// SPDX-License-Identifier:Apache-2.0

package frr

import (
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-kit/log"
	frrv1beta1 "github.com/metallb/frrk8s/api/v1beta1"
	"go.universe.tf/metallb/internal/bgp"
	"go.universe.tf/metallb/internal/bgp/frr"
	metallbconfig "go.universe.tf/metallb/internal/config"
	"go.universe.tf/metallb/internal/ipfamily"
	"go.universe.tf/metallb/internal/logging"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// As the MetalLB controller should handle messages synchronously, there should
// no need to lock this data structure. TODO: confirm this.

type sessionManager struct {
	sessions map[string]*session
	//bfdProfiles  []BFDProfile
	client           client.Client
	metallbNamespace string
	currentNode      string
	extraConfig      string
	logLevel         string
	sync.Mutex
}

type session struct {
	bgp.SessionParameters
	sessionManager *sessionManager
	advertised     []*bgp.Advertisement
	logger         log.Logger
}

// Create a variable for os.Hostname() in order to make it easy to mock out
// in unit tests.
var osHostname = os.Hostname

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
	config, err := s.sessionManager.createConfig()
	if err != nil {
		s.advertised = oldAdvs
		return err
	}

	err = s.sessionManager.writeFRRConfig(config)
	if err != nil {
		return fmt.Errorf("session %s failed to write config on set: %w", sessionName, err)
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

	frrConfig, err := s.sessionManager.createConfig()
	if err != nil {
		return err
	}

	err = s.sessionManager.writeFRRConfig(frrConfig)
	if err != nil {
		sessionName := sessionName(*s)
		return fmt.Errorf("session %s failed to write config on close: %w", sessionName, err)
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

	frrConfig, err := sm.createConfig()
	if err != nil {
		_ = sm.deleteSession(s)
		return nil, err
	}

	err = s.sessionManager.writeFRRConfig(frrConfig)
	if err != nil {
		return nil, fmt.Errorf("session %s failed to write config on new: %w", sessionName(*s), err)
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
	// TODO this is going to be deprecated
	/*
		sm.Lock()
		defer sm.Unlock()
		sm.extraConfig = extraInfo
		frrConfig, err := sm.createConfig()
		if err != nil {
			return err
		}

		sm.reloadConfig <- reloadEvent{config: frrConfig}
	*/
	return nil
}

func (sm *sessionManager) SyncBFDProfiles(profiles map[string]*metallbconfig.BFDProfile) error {
	/*
		sm.Lock()
		defer sm.Unlock()
		sm.bfdProfiles = make([]BFDProfile, 0)
		for _, p := range profiles {
			frrProfile := configBFDProfileToFRR(p)
			sm.bfdProfiles = append(sm.bfdProfiles, *frrProfile)
		}
		sort.Slice(sm.bfdProfiles, func(i, j int) bool {
			return sm.bfdProfiles[i].Name < sm.bfdProfiles[j].Name
		})

		frrConfig, err := sm.createConfig()
		if err != nil {
			return err
		}

		sm.reloadConfig <- reloadEvent{config: frrConfig}
	*/
	return nil
}

func (sm *sessionManager) createConfig() (frrv1beta1.FRRConfiguration, error) {
	res := frrv1beta1.FRRConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metallb-" + sm.currentNode,
			Namespace: sm.metallbNamespace,
		},
		Spec: frrv1beta1.FRRConfigurationSpec{
			NodeName: sm.currentNode,
		},
	}

	type router struct {
		myASN        uint32
		routerID     string
		neighbors    map[string]frrv1beta1.Neighbor
		vrf          string
		ipV4Prefixes map[string]string
		ipV6Prefixes map[string]string
	}

	routers := make(map[string]*router)

	for _, s := range sm.sessions {
		var neighbor frrv1beta1.Neighbor
		var exist bool
		var rout *router

		routerName := frr.RouterName(s.RouterID.String(), s.MyASN, s.VRFName)
		if rout, exist = routers[routerName]; !exist {
			rout = &router{
				myASN:        s.MyASN,
				neighbors:    make(map[string]frrv1beta1.Neighbor),
				ipV4Prefixes: make(map[string]string),
				ipV6Prefixes: make(map[string]string),
				vrf:          s.VRFName,
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
				return frrv1beta1.FRRConfiguration{}, err
			}

			portUint, err := strconv.ParseUint(port, 10, 16)
			if err != nil {
				return frrv1beta1.FRRConfiguration{}, err
			}

			neighbor = frrv1beta1.Neighbor{
				ASN:     s.PeerASN,
				Address: host,
				Port:    uint16(portUint),
				// HoldTime:       uint64(s.HoldTime / time.Second),
				// KeepaliveTime:  uint64(s.KeepAliveTime / time.Second),
				Password: s.Password,
				AllowedOutPrefixes: frrv1beta1.AllowedPrefixes{
					Prefixes: make([]string, 0),
				},
				// BFDProfile:   s.BFDProfile,
				// EBGPMultiHop: s.EBGPMultiHop,
				// VRFName:      s.VRFName,
			}
			//if s.SourceAddress != nil {
			//	neighbor.SrcAddr = s.SourceAddress.String()
			//}
		}

		/* As 'session.advertised' is a map, we can be sure there are no
		   duplicate prefixes and can, therefore, just add them to the
		   'neighbor.Advertisements' list. */
		for _, adv := range s.advertised {
			if !adv.MatchesPeer(s.SessionName) {
				continue
			}

			family := ipfamily.ForAddress(adv.Prefix.IP)

			communities := make([]string, 0)

			// Convert community 32bits value to : format
			for _, c := range adv.Communities {
				community := metallbconfig.CommunityToString(c)
				communities = append(communities, community)
			}

			prefix := adv.Prefix.String()
			/*
				advConfig := advertisementConfig{
					IPFamily:    family,
					Prefix:      prefix,
					Communities: communities,
					LocalPref:   adv.LocalPref,
				}
			*/
			neighbor.AllowedOutPrefixes.Prefixes = append(neighbor.AllowedOutPrefixes.Prefixes, prefix)

			switch family {
			case ipfamily.IPv4:
				rout.ipV4Prefixes[prefix] = prefix
			case ipfamily.IPv6:
				rout.ipV6Prefixes[prefix] = prefix
			}
		}
		rout.neighbors[neighborName] = neighbor
	}

	for _, r := range sortMap(routers) {
		toAdd := frrv1beta1.Router{
			ASN:        r.myASN,
			ID:         r.routerID,
			VRF:        r.vrf,
			Neighbors:  sortMap(r.neighbors),
			PrefixesV4: sortMap(r.ipV4Prefixes),
			PrefixesV6: sortMap(r.ipV6Prefixes),
		}
		res.Spec.Routers = append(res.Spec.Routers, toAdd)
	}
	return res, nil
}

func (sm *sessionManager) writeFRRConfig(config frrv1beta1.FRRConfiguration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	currentConfig := &frrv1beta1.FRRConfiguration{}
	key := client.ObjectKey{Name: config.Name, Namespace: config.Namespace}
	err := sm.client.Get(ctx, key, currentConfig)
	if errors.IsNotFound(err) {
		err = sm.client.Create(ctx, &config)
		if err != nil {
			return &bgp.TemporaryError{Err: err}
		}
		return nil
	}

	currentConfig.Spec = config.Spec
	err = sm.client.Update(ctx, currentConfig)
	if err != nil {
		return &bgp.TemporaryError{Err: err}
	}
	return nil
}

var debounceTimeout = 3 * time.Second
var failureTimeout = time.Second * 5

func NewSessionManager(l log.Logger, cli client.Client, node, namespace string, logLevel logging.Level) bgp.SessionManager {
	res := &sessionManager{
		sessions:         map[string]*session{},
		client:           cli,
		logLevel:         logLevelToFRR(logLevel),
		currentNode:      node,
		metallbNamespace: namespace,
	}

	return res
}

/*
func configBFDProfileToFRR(p *metallbconfig.BFDProfile) *BFDProfile {
	res := &BFDProfile{}
	res.Name = p.Name
	res.ReceiveInterval = p.ReceiveInterval
	res.TransmitInterval = p.TransmitInterval
	res.DetectMultiplier = p.DetectMultiplier
	res.EchoInterval = p.EchoInterval
	res.EchoMode = p.EchoMode
	res.PassiveMode = p.PassiveMode
	res.MinimumTTL = p.MinimumTTL
	return res
}
*/

func logLevelToFRR(level logging.Level) string {
	// Allowed frr log levels are: emergencies, alerts, critical,
	// 		errors, warnings, notifications, informational, or debugging
	switch level {
	case logging.LevelAll, logging.LevelDebug:
		return "debugging"
	case logging.LevelInfo:
		return "informational"
	case logging.LevelWarn:
		return "warnings"
	case logging.LevelError:
		return "error"
	case logging.LevelNone:
		return "emergencies"
	}

	return "informational"
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
