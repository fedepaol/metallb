// SPDX-License-Identifier:Apache-2.0

package frr

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-kit/log"
	frrv1beta1 "github.com/metallb/frrk8s/api/v1beta1"
	"go.universe.tf/metallb/internal/bgp"
	"go.universe.tf/metallb/internal/bgp/frr"
	metallbconfig "go.universe.tf/metallb/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type sessionManager struct {
	sessions         map[string]*session
	bfdProfiles      []frr.BFDProfile
	metallbNamespace string
	currentNode      string
	sync.Mutex
	desiredConfig         frrv1beta1.FRRConfiguration
	configChangedCallback func(interface{})
}

func (sm *sessionManager) SetEventListener(callback func(interface{})) {
	sm.Lock()
	defer sm.Unlock()
	sm.configChangedCallback = callback
}

type session struct {
	bgp.SessionParameters
	sessionManager *sessionManager
	advertised     []*bgp.Advertisement
	logger         log.Logger
}

// sessionName() defines the format of the key of the 'sessions' map in
// the 'frrState' struct.
func sessionName(s session) string {
	baseName := fmt.Sprintf("%d@%s-%d@%s", s.PeerASN, s.PeerAddress, s.MyASN, s.SourceAddress)
	if s.VRFName == "" {
		return baseName
	}
	return baseName + "/" + s.VRFName
}

func validate(adv *bgp.Advertisement) error {
	if len(adv.Communities) > 63 {
		return fmt.Errorf("max supported communities is 63, got %d", len(adv.Communities))
	}
	return nil
}

func (s *session) Set(advs ...*bgp.Advertisement) error {
	s.sessionManager.Lock()
	defer s.sessionManager.Unlock()
	sessionName := sessionName(*s)
	if _, found := s.sessionManager.sessions[sessionName]; !found {
		return fmt.Errorf("session %s not established before advertisement", sessionName)
	}

	newAdvs := []*bgp.Advertisement{}
	for _, adv := range advs {
		err := validate(adv)
		if err != nil {
			return err
		}
		newAdvs = append(newAdvs, adv)
	}
	oldAdvs := s.advertised
	s.advertised = newAdvs

	// Attempt to create a config
	err := s.sessionManager.updateConfig()
	if err != nil {
		s.advertised = oldAdvs
		return err
	}

	return nil
}

// Close() shuts down the BGP session.
func (s *session) Close() error {
	s.sessionManager.Lock()
	defer s.sessionManager.Unlock()
	err := s.sessionManager.deleteSession(s)
	if err != nil {
		return err
	}

	err = s.sessionManager.updateConfig()
	if err != nil {
		return err
	}

	return nil
}

// NewSession() creates a BGP session using the given session parameters.
//
// The session will immediately try to connect and synchronize its
// local state with the peer.
func (sm *sessionManager) NewSession(l log.Logger, args bgp.SessionParameters) (bgp.Session, error) {
	sm.Lock()
	defer sm.Unlock()
	s := &session{
		logger:            log.With(l, "peer", args.PeerAddress, "localASN", args.MyASN, "peerASN", args.PeerASN),
		advertised:        []*bgp.Advertisement{},
		sessionManager:    sm,
		SessionParameters: args,
	}

	_ = sm.addSession(s)

	err := sm.updateConfig()
	if err != nil {
		_ = sm.deleteSession(s)
		return nil, err
	}

	return s, nil
}

func (sm *sessionManager) addSession(s *session) error {
	if s == nil {
		return fmt.Errorf("invalid session")
	}
	sessionName := sessionName(*s)
	sm.sessions[sessionName] = s

	return nil
}

func (sm *sessionManager) deleteSession(s *session) error {
	if s == nil {
		return fmt.Errorf("invalid session")
	}
	sessionName := sessionName(*s)
	delete(sm.sessions, sessionName)

	return nil
}

func (sm *sessionManager) SyncExtraInfo(extraInfo string) error {
	return errors.New("extra info not supported in frr-k8s mode")
}

func (sm *sessionManager) SyncBFDProfiles(profiles map[string]*metallbconfig.BFDProfile) error {
	sm.Lock()
	defer sm.Unlock()
	sm.bfdProfiles = make([]frr.BFDProfile, 0)
	for _, p := range profiles {
		frrProfile := frr.ConfigBFDProfileToFRR(p)
		sm.bfdProfiles = append(sm.bfdProfiles, *frrProfile)
	}
	sort.Slice(sm.bfdProfiles, func(i, j int) bool {
		return sm.bfdProfiles[i].Name < sm.bfdProfiles[j].Name
	})

	err := sm.updateConfig()
	if err != nil {
		return err
	}
	return nil
}

func (sm *sessionManager) updateConfig() error {
	newConfig := frrv1beta1.FRRConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigName(sm.currentNode),
			Namespace: sm.metallbNamespace,
		},
		Spec: frrv1beta1.FRRConfigurationSpec{
			NodeSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kubernetes.io/hostname": sm.currentNode,
				},
			},
			BGP: frrv1beta1.BGPConfig{
				Routers:     make([]frrv1beta1.Router, 0),
				BFDProfiles: make([]frrv1beta1.BFDProfile, 0),
			},
		},
	}

	type router struct {
		myASN     uint32
		routerID  string
		neighbors map[string]frrv1beta1.Neighbor
		vrf       string
		prefixes  map[string]string
	}

	routers := make(map[string]*router)

	for _, s := range sm.sessions {
		var neighbor frrv1beta1.Neighbor
		var exist bool
		var rout *router

		routerName := frr.RouterName(s.RouterID.String(), s.MyASN, s.VRFName)
		if rout, exist = routers[routerName]; !exist {
			rout = &router{
				myASN:     s.MyASN,
				neighbors: make(map[string]frrv1beta1.Neighbor),
				prefixes:  make(map[string]string),
				vrf:       s.VRFName,
			}
			if s.RouterID != nil {
				rout.routerID = s.RouterID.String()
			}
			routers[routerName] = rout
		}

		neighborName := frr.NeighborName(s.PeerAddress, s.PeerASN, s.VRFName)
		if neighbor, exist = rout.neighbors[neighborName]; !exist {
			host, port, err := net.SplitHostPort(s.PeerAddress)
			if err != nil {
				return err
			}

			portUint, err := strconv.ParseUint(port, 10, 16)
			if err != nil {
				return err
			}

			neighbor = frrv1beta1.Neighbor{
				ASN:           s.PeerASN,
				Address:       host,
				Port:          uint16(portUint),
				HoldTime:      metav1.Duration{Duration: s.HoldTime},
				KeepaliveTime: metav1.Duration{Duration: s.KeepAliveTime},
				BFDProfile:    s.BFDProfile,
				EBGPMultiHop:  s.EBGPMultiHop,
				ToAdvertise: frrv1beta1.Advertise{
					Allowed: frrv1beta1.AllowedOutPrefixes{
						Prefixes: make([]string, 0),
					},
					PrefixesWithLocalPref: make([]frrv1beta1.LocalPrefPrefixes, 0),
					PrefixesWithCommunity: make([]frrv1beta1.CommunityPrefixes, 0),
				},
				// TODO password
			}
		}

		/* As 'session.advertised' is a map, we can be sure there are no
		   duplicate prefixes and can, therefore, just add them to the
		   'neighbor.Advertisements' list. */
		prefixesForCommunity := map[string][]string{}
		prefixesForLocalPref := map[uint32][]string{}
		for _, adv := range s.advertised {
			if !adv.MatchesPeer(s.SessionName) {
				continue
			}

			prefix := adv.Prefix.String()
			neighbor.ToAdvertise.Allowed.Prefixes = append(neighbor.ToAdvertise.Allowed.Prefixes, prefix)
			rout.prefixes[prefix] = prefix

			for _, c := range adv.Communities {
				community := c.String()
				prefixesForCommunity[community] = append(prefixesForCommunity[community], prefix)
			}
			if adv.LocalPref != 0 {
				prefixesForLocalPref[adv.LocalPref] = append(prefixesForLocalPref[adv.LocalPref], prefix)
			}

		}
		for c, prefixes := range prefixesForCommunity {
			neighbor.ToAdvertise.PrefixesWithCommunity = append(neighbor.ToAdvertise.PrefixesWithCommunity,
				frrv1beta1.CommunityPrefixes{Community: c, Prefixes: prefixes})
		}
		sort.Slice(neighbor.ToAdvertise.PrefixesWithCommunity, func(i, j int) bool {
			return neighbor.ToAdvertise.PrefixesWithCommunity[i].Community > neighbor.ToAdvertise.PrefixesWithCommunity[j].Community
		})
		for l, prefixes := range prefixesForLocalPref {
			neighbor.ToAdvertise.PrefixesWithLocalPref = append(neighbor.ToAdvertise.PrefixesWithLocalPref,
				frrv1beta1.LocalPrefPrefixes{LocalPref: l, Prefixes: prefixes})
		}
		sort.Slice(neighbor.ToAdvertise.PrefixesWithLocalPref, func(i, j int) bool {
			return neighbor.ToAdvertise.PrefixesWithLocalPref[i].LocalPref > neighbor.ToAdvertise.PrefixesWithLocalPref[j].LocalPref
		})

		rout.neighbors[neighborName] = neighbor
	}

	for _, r := range sortMap(routers) {
		toAdd := frrv1beta1.Router{
			ASN:       r.myASN,
			ID:        r.routerID,
			VRF:       r.vrf,
			Neighbors: sortMap(r.neighbors),
			Prefixes:  sortMap(r.prefixes),
		}
		newConfig.Spec.BGP.Routers = append(newConfig.Spec.BGP.Routers, toAdd)
	}

	for _, bfd := range sm.bfdProfiles {
		toAdd := frrv1beta1.BFDProfile{
			Name: bfd.Name,
		}
		if bfd.ReceiveInterval != nil {
			toAdd.ReceiveInterval = *bfd.ReceiveInterval
		}
		if bfd.TransmitInterval != nil {
			toAdd.TransmitInterval = *bfd.TransmitInterval
		}
		if bfd.DetectMultiplier != nil {
			toAdd.TransmitInterval = *bfd.TransmitInterval
		}
		if bfd.EchoInterval != nil {
			toAdd.EchoInterval = *bfd.EchoInterval
		}
		if bfd.MinimumTTL != nil {
			toAdd.MinimumTTL = *bfd.MinimumTTL
		}
		toAdd.EchoMode = bfd.EchoMode
		toAdd.PassiveMode = bfd.PassiveMode

		newConfig.Spec.BGP.BFDProfiles = append(newConfig.Spec.BGP.BFDProfiles, toAdd)

	}
	sm.desiredConfig = newConfig
	toNotify := sm.desiredConfig.DeepCopy()
	sm.configChangedCallback(*toNotify)
	return nil
}

var debounceTimeout = 3 * time.Second

func NewSessionManager(l log.Logger, node, namespace string) bgp.SessionManager {
	res := &sessionManager{
		sessions:         map[string]*session{},
		currentNode:      node,
		metallbNamespace: namespace,
	}

	return res
}

// debouncer takes a function representing a callback, a channel where
// the update requests are sent, and squashes any requests coming in a given timeframe
// as a single callback.
func debouncer(body func(),
	reload <-chan struct{},
	reloadInterval time.Duration) {
	go func() {
		var timeOut <-chan time.Time
		timerSet := false
		for {
			select {
			case _, ok := <-reload:
				if !ok { // the channel was closed
					return
				}
				if !timerSet {
					timeOut = time.After(reloadInterval)
					timerSet = true
				}
			case <-timeOut:
				body()
				timerSet = false
			}
		}
	}()
}

func ConfigName(node string) string {
	return "metallb-" + node
}

func sortMap[T any](toSort map[string]T) []T {
	keys := make([]string, 0)
	for k := range toSort {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	res := make([]T, 0)
	for _, k := range keys {
		res = append(res, toSort[k])
	}
	return res
}
