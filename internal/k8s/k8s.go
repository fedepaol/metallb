// SPDX-License-Identifier:Apache-2.0

package k8s // import "go.universe.tf/metallb/internal/k8s"

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"

	metallbv1alpha1 "go.universe.tf/metallb/api/v1alpha1"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"go.universe.tf/metallb/controllers"
	"go.universe.tf/metallb/internal/config"
	"go.universe.tf/metallb/internal/k8s/epslices"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	policyv1beta1 "k8s.io/kubernetes/pkg/apis/policy/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	rbacv1 "k8s.io/kubernetes/pkg/apis/rbac/v1"

	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(metallbv1alpha1.AddToScheme(scheme))
	utilruntime.Must(metallbv1beta1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(policyv1beta1.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))
	utilruntime.Must(apiext.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

// Client watches a Kubernetes cluster and translates events into
// Controller method calls.
type Client struct {
	logger log.Logger

	client *kubernetes.Clientset
	events record.EventRecorder
	queue  workqueue.RateLimitingInterface

	svcIndexer     cache.Indexer
	svcInformer    cache.Controller
	epIndexer      cache.Indexer
	epInformer     cache.Controller
	slicesIndexer  cache.Indexer
	slicesInformer cache.Controller
	cmIndexer      cache.Indexer
	cmInformer     cache.Controller
	nodeIndexer    cache.Indexer
	nodeInformer   cache.Controller

	syncFuncs []cache.InformerSynced

	serviceChanged func(log.Logger, string, *v1.Service, epslices.EpsOrSlices) controllers.SyncState
	configChanged  func(log.Logger, *config.Config) controllers.SyncState
	validateConfig config.Validate
	nodeChanged    func(log.Logger, *v1.Node) controllers.SyncState
	synced         func(log.Logger)
}

// Config specifies the configuration of the Kubernetes
// client/watcher.
type Config struct {
	ProcessName     string
	ConfigMapName   string
	ConfigMapNS     string
	NodeName        string
	MetricsHost     string
	MetricsPort     int
	EnablePprof     bool
	ReadEndpoints   bool
	Logger          log.Logger
	Kubeconfig      string
	DisableEpSlices bool
	Namespace       string

	ServiceChanged func(log.Logger, string, *v1.Service, epslices.EpsOrSlices) controllers.SyncState
	ConfigChanged  func(log.Logger, *config.Config) controllers.SyncState
	ValidateConfig config.Validate
	NodeChanged    func(log.Logger, *v1.Node) controllers.SyncState
	Synced         func(log.Logger)
}

type svcKey string
type cmKey string
type nodeKey string
type synced string

// New connects to masterAddr, using kubeconfig to authenticate.
//
// The client uses processName to identify itself to the cluster
// (e.g. when logging events).
//nolint:godot
func New(cfg *Config) (*Client, error) {
	var (
		k8sConfig *rest.Config
		err       error
	)

	if cfg.Kubeconfig == "" {
		// if the user didn't provide a config file, assume that we're
		// running inside k8s.
		k8sConfig, err = rest.InClusterConfig()
	} else {
		// the user provided a config file, so use that.  InClusterConfig
		// would also work in this case but it emits an annoying warning.
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("building client config: %s", err)
	}
	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes client: %s", err)
	}

	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(clientset.CoreV1().RESTClient()).Events("")})
	recorder := broadcaster.NewRecorder(scheme, v1.EventSource{Component: cfg.ProcessName})

	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	c := &Client{
		logger:         cfg.Logger,
		client:         clientset,
		events:         recorder,
		queue:          queue,
		validateConfig: cfg.ValidateConfig,
	}
	/*
		if cfg.ServiceChanged != nil {
			svcHandlers := cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					key, err := cache.MetaNamespaceKeyFunc(obj)
					if err == nil {
						c.queue.Add(svcKey(key))
					}
				},
				UpdateFunc: func(old interface{}, new interface{}) {
					key, err := cache.MetaNamespaceKeyFunc(new)
					if err == nil {
						c.queue.Add(svcKey(key))
					}
				},
				DeleteFunc: func(obj interface{}) {
					key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
					if err == nil {
						c.queue.Add(svcKey(key))
					}
				},
			}
			svcWatcher := cache.NewListWatchFromClient(c.client.CoreV1().RESTClient(), "services", v1.NamespaceAll, fields.Everything())
			c.svcIndexer, c.svcInformer = cache.NewIndexerInformer(svcWatcher, &v1.Service{}, 0, svcHandlers, cache.Indexers{})

			c.serviceChanged = cfg.ServiceChanged
			c.syncFuncs = append(c.syncFuncs, c.svcInformer.HasSynced)

			if cfg.ReadEndpoints {
				// use DisableEpSlices to skip the autodiscovery mechanism. Useful if EndpointSlices are enabled in the cluster but disabled in kube-proxy
				if cfg.DisableEpSlices || !UseEndpointSlices(c.client) {
					epHandlers := cache.ResourceEventHandlerFuncs{
						AddFunc: func(obj interface{}) {
							key, err := cache.MetaNamespaceKeyFunc(obj)
							if err == nil {
								c.queue.Add(svcKey(key))
							}
						},
						UpdateFunc: func(old interface{}, new interface{}) {
							key, err := cache.MetaNamespaceKeyFunc(new)
							if err == nil {
								c.queue.Add(svcKey(key))
							}
						},
						DeleteFunc: func(obj interface{}) {
							key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
							if err == nil {
								c.queue.Add(svcKey(key))
							}
						},
					}
					epWatcher := cache.NewListWatchFromClient(c.client.CoreV1().RESTClient(), "endpoints", v1.NamespaceAll, fields.Everything())
					c.epIndexer, c.epInformer = cache.NewIndexerInformer(epWatcher, &v1.Endpoints{}, 0, epHandlers, cache.Indexers{})

					c.syncFuncs = append(c.syncFuncs, c.epInformer.HasSynced)
				} else {
					level.Info(c.logger).Log("op", "New", "msg", "using endpoint slices")
					slicesHandlers := cache.ResourceEventHandlerFuncs{
						AddFunc: func(obj interface{}) {
							slice, ok := obj.(*discovery.EndpointSlice)
							if !ok {
								level.Error(c.logger).Log("op", "SliceAdd", "error", "received a non EndpointSlice item")
								return
							}

							key, err := ServiceKeyForSlice(slice)
							if err != nil {
								return
							}
							c.queue.Add(key)
						},
						UpdateFunc: func(old interface{}, new interface{}) {
							slice, ok := new.(*discovery.EndpointSlice)
							if !ok {
								level.Error(c.logger).Log("op", "SliceUpdate", "error", "received a non EndpointSlice item")
								return
							}
							key, err := ServiceKeyForSlice(slice)
							if err != nil {
								level.Error(c.logger).Log("op", "SliceUpdate", "error", "failed to get serviceKey for slice", "slice", slice.Name)
								return
							}
							c.queue.Add(key)
						},
						DeleteFunc: func(obj interface{}) {
							slice, ok := obj.(*discovery.EndpointSlice)
							if !ok {
								level.Error(c.logger).Log("op", "SliceDelete", "error", "received a non EndpointSlice item")
								return
							}
							key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(slice)
							if err != nil {
								level.Error(c.logger).Log("op", "SliceDelete", "error", err)
								return
							}
							c.queue.Add(svcKey(key))
						},
					}
					slicesWatcher := cache.NewListWatchFromClient(c.client.DiscoveryV1beta1().RESTClient(), "endpointslices", v1.NamespaceAll, fields.Everything())
					c.slicesIndexer, c.slicesInformer = cache.NewIndexerInformer(slicesWatcher, &discovery.EndpointSlice{}, 0, slicesHandlers, cache.Indexers{
						SlicesServiceIndexName: slicesServiceIndex,
					})
					c.syncFuncs = append(c.syncFuncs, c.slicesInformer.HasSynced)
				}
			}
		}
	*/
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		// MetricsBindAddress: metricsAddr,
		Port:           9443, // TODO port only with controller
		LeaderElection: false,
		Namespace:      cfg.Namespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	if cfg.ConfigChanged != nil {
		if err = (&controllers.ConfigReconciler{
			Client:    mgr.GetClient(),
			Log:       cfg.Logger,
			Scheme:    mgr.GetScheme(),
			Namespace: cfg.Namespace,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
			os.Exit(1)
		}
	}

	if cfg.NodeChanged != nil {
		if err = (&controllers.NodeReconciler{
			Client:  mgr.GetClient(),
			Log:     cfg.Logger,
			Scheme:  mgr.GetScheme(),
			Handler: cfg.NodeChanged,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
			os.Exit(1)
		}
	}

	if cfg.ServiceChanged != nil {
		if err = (&controllers.ServiceReconciler{
			Client:  mgr.GetClient(),
			Log:     cfg.Logger,
			Scheme:  mgr.GetScheme(),
			Handler: cfg.ServiceChanged,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
			os.Exit(1)
		}

		if cfg.ReadEndpoints {
			// use DisableEpSlices to skip the autodiscovery mechanism. Useful if EndpointSlices are enabled in the cluster but disabled in kube-proxy
			if cfg.DisableEpSlices || !UseEndpointSlices(c.client) {
				// Todo endpoints
			} else {
				if err = (&controllers.EpSlicesReconciler{
					Client:  mgr.GetClient(),
					Log:     cfg.Logger,
					Scheme:  mgr.GetScheme(),
					Handler: cfg.ServiceChanged,
				}).SetupWithManager(mgr); err != nil {
					setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
					os.Exit(1)
				}
				if err := mgr.GetFieldIndexer().IndexField(context.Background(), &discovery.EndpointSlice{}, epslices.SlicesServiceIndexName, func(rawObj client.Object) []string {
					res, err := epslices.SlicesServiceIndex(rawObj)
					if err != nil {
						return []string{}
					}
					return res
				}); err != nil {
					return c, err
				}
			}
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	if cfg.EnablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	go func(l log.Logger) {
		err := http.ListenAndServe(net.JoinHostPort(cfg.MetricsHost, fmt.Sprint(cfg.MetricsPort)), mux)
		if err != nil {
			level.Error(l).Log("op", "listenAndServe", "err", err, "msg", "cannot listen and serve", "host", cfg.MetricsHost, "port", cfg.MetricsPort)
		}
	}(c.logger)

	return c, nil
}

// CreateMlSecret create the memberlist secret.
func (c *Client) CreateMlSecret(namespace, controllerDeploymentName, secretName string) error {
	// Use List instead of Get to differentiate between API errors and non existing secret.
	// Matching error text is prone to future breakage.
	l, err := c.client.CoreV1().Secrets(namespace).List(context.TODO(), metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", secretName).String(),
	})
	if err != nil {
		return err
	}
	if len(l.Items) > 0 {
		level.Debug(c.logger).Log("op", "CreateMlSecret", "msg", "secret already exists, nothing to do")
		return nil
	}

	// Get the controller Deployment info to set secret ownerReference.
	d, err := c.client.AppsV1().Deployments(namespace).Get(context.TODO(), controllerDeploymentName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Create the secret key (128 bits).
	secret := make([]byte, 16)
	_, err = rand.Read(secret)
	if err != nil {
		return err
	}
	// base64 encode the secret key as it'll be passed a env variable.
	secretB64 := make([]byte, base64.RawStdEncoding.EncodedLen(len(secret)))
	base64.RawStdEncoding.Encode(secretB64, secret)

	// Create the K8S Secret object.
	_, err = c.client.CoreV1().Secrets(namespace).Create(
		context.TODO(),
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretName,
				OwnerReferences: []metav1.OwnerReference{{
					// d.APIVersion is empty.
					APIVersion: "apps/v1",
					// d.Kind is empty.
					Kind: "Deployment",
					Name: d.Name,
					UID:  d.UID,
				}},
			},
			Data: map[string][]byte{"secretkey": secretB64},
		},
		metav1.CreateOptions{})
	if err == nil {
		level.Info(c.logger).Log("op", "CreateMlSecret", "msg", "secret succesfully created")
	}
	return err
}

// PodIPs returns the IPs of all the pods matched by the labels string.
func (c *Client) PodIPs(namespace, labels string) ([]string, error) {
	pl, err := c.client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: labels})
	if err != nil {
		return nil, err
	}
	iplist := []string{}
	for _, pod := range pl.Items {
		iplist = append(iplist, pod.Status.PodIP)
	}
	return iplist, nil
}

// Run watches for events on the Kubernetes cluster, and dispatches
// calls to the Controller.
func (c *Client) Run(stopCh <-chan struct{}) error {
	if c.svcInformer != nil {
		go c.svcInformer.Run(stopCh)
	}
	if c.epInformer != nil {
		go c.epInformer.Run(stopCh)
	}
	if c.slicesInformer != nil {
		go c.slicesInformer.Run(stopCh)
	}
	if c.cmInformer != nil {
		go c.cmInformer.Run(stopCh)
	}
	if c.nodeInformer != nil {
		go c.nodeInformer.Run(stopCh)
	}

	if !cache.WaitForCacheSync(stopCh, c.syncFuncs...) {
		return errors.New("timed out waiting for cache sync")
	}

	c.queue.Add(synced(""))

	if stopCh != nil {
		go func() {
			<-stopCh
			c.queue.ShutDown()
		}()
	}

	for {
		key, quit := c.queue.Get()
		if quit {
			return nil
		}
		updates.Inc()
		fmt.Println(key)
		// st := c.sync(key)
		//switch st {
		//case SyncStateErrorNoRetry, SyncStateSuccess:
		//	c.queue.Forget(key)
		//case SyncStateError:
		//	updateErrors.Inc()
		//	c.queue.AddRateLimited(key)
		//case SyncStateReprocessAll:
		//	c.queue.Forget(key)
		//	c.ForceSync()
		//}
	}
}

// ForceSync reprocess all watched services.
func (c *Client) ForceSync() {
	if c.svcIndexer != nil {
		for _, k := range c.svcIndexer.ListKeys() {
			c.queue.AddRateLimited(svcKey(k))
		}
	}
}

// UpdateStatus writes the protected "status" field of svc back into
// the Kubernetes cluster.
func (c *Client) UpdateStatus(svc *v1.Service) error {
	_, err := c.client.CoreV1().Services(svc.Namespace).UpdateStatus(context.TODO(), svc, metav1.UpdateOptions{})
	return err
}

// Infof logs an informational event about svc to the Kubernetes cluster.
func (c *Client) Infof(svc *v1.Service, kind, msg string, args ...interface{}) {
	c.events.Eventf(svc, v1.EventTypeNormal, kind, msg, args...)
}

// Errorf logs an error event about svc to the Kubernetes cluster.
func (c *Client) Errorf(svc *v1.Service, kind, msg string, args ...interface{}) {
	c.events.Eventf(svc, v1.EventTypeWarning, kind, msg, args...)
}

/*
func (c *Client) sync(key interface{}) SyncState {
	defer c.queue.Done(key)

	switch k := key.(type) {
	case svcKey:
		l := log.With(c.logger, "service", string(k))
		svc, exists, err := c.svcIndexer.GetByKey(string(k))
		if err != nil {
			level.Error(l).Log("op", "getService", "error", err, "msg", "failed to get service")
			return SyncStateError
		}
		if !exists {
			return c.serviceChanged(l, string(k), nil, EpsOrSlices{})
		}

		epsOrSlices := EpsOrSlices{}
		if c.epIndexer != nil {
			epsIntf, exists, err := c.epIndexer.GetByKey(string(k))
			if err != nil {
				level.Error(l).Log("op", "getEndpoints", "error", err, "msg", "failed to get endpoints")
				return SyncStateError
			}
			if !exists {
				return c.serviceChanged(l, string(k), nil, EpsOrSlices{})
			}

			eps := epsIntf.(*v1.Endpoints)
			epsOrSlices.EpVal = eps.DeepCopy()
			epsOrSlices.Type = Eps
		}
		if c.slicesIndexer != nil {
			slicesIntf, err := c.slicesIndexer.ByIndex(SlicesServiceIndexName, string(k))
			if err != nil {
				level.Error(l).Log("op", "getEndpointSlices", "error", err, "msg", "failed to get endpoints slices")
				return SyncStateError
			}
			if len(slicesIntf) == 0 {
				return c.serviceChanged(l, string(k), nil, EpsOrSlices{})
			}
			epsOrSlices.SlicesVal = make([]*discovery.EndpointSlice, 0)
			for _, s := range slicesIntf {
				slice, ok := s.(*discovery.EndpointSlice)
				if !ok {
					continue
				}
				epsOrSlices.SlicesVal = append(epsOrSlices.SlicesVal, slice.DeepCopy())
			}
			epsOrSlices.Type = Slices
		}
		return c.serviceChanged(l, string(k), svc.(*v1.Service), epsOrSlices)

	case cmKey:
		l := log.With(c.logger, "configmap", string(k))
		cmi, exists, err := c.cmIndexer.GetByKey(string(k))
		if err != nil {
			level.Error(l).Log("op", "getConfigMap", "error", err, "msg", "failed to get configmap")
			return SyncStateError
		}
		if !exists {
			configStale.Set(1)
			return c.configChanged(l, nil)
		}

		// Note that configs that we can read, but that fail parsing
		// or validation, result in a "synced" state, because the
		// config is not going to parse any better until the k8s
		// object changes to fix the issue.
		cm := cmi.(*v1.ConfigMap)
		cfg, err := config.Parse([]byte(cm.Data["config"]), c.validateConfig)
		if err != nil {
			level.Error(l).Log("event", "configStale", "error", err, "msg", "config (re)load failed, config marked stale")
			configStale.Set(1)
			return SyncStateSuccess
		}

		st := c.configChanged(l, cfg)
		if st == SyncStateErrorNoRetry || st == SyncStateError {
			level.Error(l).Log("event", "configStale", "error", err, "msg", "config (re)load failed, config marked stale")
			configStale.Set(1)
			return st
		}

		configLoaded.Set(1)
		configStale.Set(0)

		level.Info(l).Log("event", "configLoaded", "msg", "config (re)loaded")
		return st

	case nodeKey:
		l := log.With(c.logger, "node", string(k))
		n, exists, err := c.nodeIndexer.GetByKey(string(k))
		if err != nil {
			level.Error(l).Log("op", "getNode", "error", err, "msg", "failed to get node")
			return SyncStateError
		}
		if !exists {
			level.Error(l).Log("op", "getNode", "error", "node doesn't exist in k8s, but I'm running on it!")
			return SyncStateError
		}
		node := n.(*v1.Node)
		return c.nodeChanged(c.logger, node)

	case synced:
		if c.synced != nil {
			c.synced(c.logger)
		}
		return SyncStateSuccess

	default:
		panic(fmt.Errorf("unknown key type for %#v (%T)", key, key))
	}
}
*/

// UseEndpointSlices detect if Endpoints Slices are enabled in the cluster.
func UseEndpointSlices(kubeClient kubernetes.Interface) bool {
	if _, err := kubeClient.Discovery().ServerResourcesForGroupVersion(discovery.SchemeGroupVersion.String()); err != nil {
		return false
	}
	// this is needed to check if ep slices are enabled on the cluster. In 1.17 the resources are there but disabled by default
	if _, err := kubeClient.DiscoveryV1beta1().EndpointSlices("default").Get(context.Background(), "kubernetes", metav1.GetOptions{}); err != nil {
		return false
	}
	return true
}
