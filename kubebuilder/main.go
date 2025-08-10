package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	syaml "sigs.k8s.io/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// Config holds target CM/Deployment
type Config struct {
	ConfigMapNamespace   string
	ConfigMapName        string
	DeploymentNamespace  string
	DeploymentName       string
	MetricsBindAddress   string
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() Config {
	return Config{
		ConfigMapNamespace:  getEnv("CONFIGMAP_NAMESPACE", "default"),
		ConfigMapName:       getEnv("CONFIGMAP_NAME", "universal-proxy"),
		DeploymentNamespace: getEnv("DEPLOYMENT_NAMESPACE", "default"),
		DeploymentName:      getEnv("DEPLOYMENT_NAME", "universal-proxy"),
		MetricsBindAddress:  getEnv("METRICS_BIND_ADDRESS", "0"), // "0" disables metrics
	}
}

func main() {
	flag.Parse()

	cfg := ctrl.GetConfigOrDie()
	conf := loadConfig()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  runtime.NewScheme(),
		Metrics: server.Options{BindAddress: conf.MetricsBindAddress},
	})
	if err != nil {
		panic(err)
	}

	r := &Reconciler{Client: mgr.GetClient(), Config: conf}
	if err := r.Setup(mgr); err != nil {
		panic(err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		panic(err)
	}
}

type Reconciler struct {
	client.Client
	Config Config
}

func (r *Reconciler) Setup(mgr ctrl.Manager) error {
	// Periodic reconcile loop
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ctx := context.Background()
			if err := r.ReconcileOnce(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "reconcile error: %v\n", err)
			}
		}
	}()
	_, err := controller.New("noop", mgr, controller.Options{Reconciler: r})
	return err
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// ReconcileOnce lists ACK resources and writes Envoy config
func (r *Reconciler) ReconcileOnce(ctx context.Context) error {
	// Discover resources
	httpRoutes := []HTTPService{}
	tcpRoutes := []TCPService{}

	if err := r.collectS3(ctx, &httpRoutes); err != nil { return err }
	if err := r.collectDynamoDB(ctx, &httpRoutes); err != nil { return err }
	if err := r.collectRDS(ctx, &tcpRoutes); err != nil { return err }
	if err := r.collectEC2(ctx, &tcpRoutes); err != nil { return err }

	// Build envoy.yaml
	envoyYAML, err := renderEnvoy(httpRoutes, tcpRoutes)
	if err != nil { return err }

	// Upsert ConfigMap
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: r.Config.ConfigMapName, Namespace: r.Config.ConfigMapNamespace}, cm); err != nil {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: r.Config.ConfigMapName, Namespace: r.Config.ConfigMapNamespace},
			Data: map[string]string{"envoy.yaml": envoyYAML},
		}
		if err := r.Create(ctx, cm); err != nil { return err }
	} else {
		if cm.Data == nil { cm.Data = map[string]string{} }
		cm.Data["envoy.yaml"] = envoyYAML
		if err := r.Update(ctx, cm); err != nil { return err }
	}

	// Patch Deployment annotation to trigger rollout
	h := sha256.Sum256([]byte(envoyYAML))
	hash := hex.EncodeToString(h[:])
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: r.Config.DeploymentName, Namespace: r.Config.DeploymentNamespace}, dep); err == nil {
		if dep.Spec.Template.Annotations == nil { dep.Spec.Template.Annotations = map[string]string{} }
		if dep.Spec.Template.Annotations["proxy.envoy/config-hash"] != hash {
			dep.Spec.Template.Annotations["proxy.envoy/config-hash"] = hash
			if err := r.Update(ctx, dep); err != nil { return err }
		}
	}
	return nil
}

// Data models
type HTTPService struct {
	Name           string
	ListenerPort   int
	UpstreamHost   string
	UpstreamPort   int
	TLS            bool
	SigV4          *SigV4
	PrefixRewrite  string
	HostRewrite    string
}

type SigV4 struct { Service, Region string; UseUnsigned bool }

type TCPService struct {
	Name         string
	ListenerPort int
	UpstreamHost string
	UpstreamPort int
	TLS          bool
}

// Collectors for ACK CRDs using dynamic/unstructured
func (r *Reconciler) collectS3(ctx context.Context, out *[]HTTPService) error {
	gvk := schema.GroupVersionKind{Group: "s3.services.k8s.aws", Version: "v1alpha1", Kind: "Bucket"}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)
	if err := r.List(ctx, list); err != nil { return nil }
	for _, item := range list.Items {
		name := item.GetName()
		region := item.GetAnnotations()["proxy.envoy/region"]
		bucket := name
		if v := item.GetAnnotations()["proxy.envoy/bucket"]; v != "" { bucket = v }
		svc := HTTPService{
			Name: fmt.Sprintf("s3-%s", name),
			ListenerPort: 18080,
			UpstreamHost: fmt.Sprintf("s3.%s.amazonaws.com", region),
			UpstreamPort: 443,
			TLS: true,
			SigV4: &SigV4{Service: "s3", Region: region, UseUnsigned: true},
			PrefixRewrite: "/" + bucket,
		}
		*out = append(*out, svc)
	}
	return nil
}

func (r *Reconciler) collectDynamoDB(ctx context.Context, out *[]HTTPService) error {
	gvk := schema.GroupVersionKind{Group: "dynamodb.services.k8s.aws", Version: "v1alpha1", Kind: "Table"}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)
	if err := r.List(ctx, list); err != nil { return nil }
	for _, item := range list.Items {
		region := item.GetAnnotations()["proxy.envoy/region"]
		name := item.GetName()
		svc := HTTPService{
			Name: fmt.Sprintf("ddb-%s", name),
			ListenerPort: 18081,
			UpstreamHost: fmt.Sprintf("dynamodb.%s.amazonaws.com", region),
			UpstreamPort: 443,
			TLS: true,
			SigV4: &SigV4{Service: "dynamodb", Region: region, UseUnsigned: false},
		}
		*out = append(*out, svc)
	}
	return nil
}

func (r *Reconciler) collectRDS(ctx context.Context, out *[]TCPService) error {
	gvk := schema.GroupVersionKind{Group: "rds.services.k8s.aws", Version: "v1alpha1", Kind: "DBInstance"}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)
	if err := r.List(ctx, list); err != nil { return nil }
	for _, item := range list.Items {
		status, _, _ := unstructured.NestedMap(item.Object, "status")
		addr, _, _ := unstructured.NestedString(status, "endpoint", "address")
		port64, _, _ := unstructured.NestedInt64(status, "endpoint", "port")
		if addr == "" || port64 == 0 { continue }
		svc := TCPService{
			Name: fmt.Sprintf("rds-%s", item.GetName()),
			ListenerPort: 15432,
			UpstreamHost: addr,
			UpstreamPort: int(port64),
			TLS: true,
		}
		*out = append(*out, svc)
	}
	return nil
}

func (r *Reconciler) collectEC2(ctx context.Context, out *[]TCPService) error {
	gvk := schema.GroupVersionKind{Group: "ec2.services.k8s.aws", Version: "v1alpha1", Kind: "Instance"}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)
	if err := r.List(ctx, list); err != nil { return nil }
	for _, item := range list.Items {
		status, _, _ := unstructured.NestedMap(item.Object, "status")
		ip, _, _ := unstructured.NestedString(status, "privateIPAddress")
		portStr := item.GetAnnotations()["proxy.envoy/port"]
		port := 8080
		if portStr != "" { fmt.Sscanf(portStr, "%d", &port) }
		if ip == "" { continue }
		svc := TCPService{
			Name: fmt.Sprintf("ec2-%s", item.GetName()),
			ListenerPort: 10000 + (len(*out) % 20000),
			UpstreamHost: ip,
			UpstreamPort: port,
			TLS: false,
		}
		*out = append(*out, svc)
	}
	return nil
}

// Render a minimal envoy.yaml using structured YAML marshalling
func renderEnvoy(httpSvcs []HTTPService, tcpSvcs []TCPService) (string, error) {
	listeners := make([]any, 0, len(httpSvcs)+len(tcpSvcs))
	clusters := make([]any, 0, len(httpSvcs)+len(tcpSvcs))

	for _, s := range httpSvcs {
		route := map[string]any{
			"match": map[string]any{"prefix": "/"},
			"route": func() map[string]any {
				r := map[string]any{"cluster": "http_upstream_" + s.Name}
				if s.PrefixRewrite != "" { r["prefix_rewrite"] = s.PrefixRewrite }
				if s.HostRewrite != "" { r["host_rewrite_literal"] = s.HostRewrite }
				return r
			}(),
		}
		if s.SigV4 != nil {
			route["typed_per_filter_config"] = map[string]any{
				"envoy.filters.http.aws_request_signing": map[string]any{
					"@type": "type.googleapis.com/envoy.extensions.filters.http.aws_request_signing.v3.AwsRequestSigningPerRoute",
					"override_config": func() map[string]any {
						m := map[string]any{"service_name": s.SigV4.Service, "region": s.SigV4.Region}
						if s.SigV4.UseUnsigned { m["use_unsigned_payload"] = true }
						return m
					}(),
				},
			}
		}

		vh := map[string]any{
			"name": "vh_" + s.Name,
			"domains": []any{"*"},
			"routes": []any{route},
		}

		l := map[string]any{
			"name": "http_" + s.Name,
			"address": map[string]any{"socket_address": map[string]any{"address": "0.0.0.0", "port_value": s.ListenerPort}},
			"filter_chains": []any{
				map[string]any{
					"filters": []any{
						map[string]any{
							"name": "envoy.filters.network.http_connection_manager",
							"typed_config": map[string]any{
								"@type": "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
								"stat_prefix": "http_" + s.Name,
								"route_config": map[string]any{
									"name": "route_" + s.Name,
									"virtual_hosts": []any{vh},
								},
								"http_filters": func() []any {
									filters := []any{}
									if s.SigV4 != nil {
										filters = append(filters, map[string]any{
											"name": "envoy.filters.http.aws_request_signing",
											"typed_config": map[string]any{
												"@type": "type.googleapis.com/envoy.extensions.filters.http.aws_request_signing.v3.AwsRequestSigning",
												"service_name": s.SigV4.Service,
												"region": s.SigV4.Region,
												"use_unsigned_payload": s.SigV4.UseUnsigned,
											},
										})
									}
									filters = append(filters, map[string]any{"name": "envoy.filters.http.router"})
									return filters
								}(),
							},
						},
					},
				},
			},
		}
		listeners = append(listeners, l)

		cluster := map[string]any{
			"name": "http_upstream_" + s.Name,
			"connect_timeout": "2s",
			"type": "LOGICAL_DNS",
			"lb_policy": "ROUND_ROBIN",
			"load_assignment": map[string]any{
				"cluster_name": "http_upstream_" + s.Name,
				"endpoints": []any{
					map[string]any{"lb_endpoints": []any{map[string]any{"endpoint": map[string]any{"address": map[string]any{"socket_address": map[string]any{"address": s.UpstreamHost, "port_value": s.UpstreamPort}}}}}},
				},
			},
		}
		if s.TLS {
			cluster["transport_socket"] = map[string]any{
				"name": "envoy.transport_sockets.tls",
				"typed_config": map[string]any{"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext", "sni": s.UpstreamHost},
			}
		}
		clusters = append(clusters, cluster)
	}

	for _, s := range tcpSvcs {
		l := map[string]any{
			"name": "tcp_" + s.Name,
			"address": map[string]any{"socket_address": map[string]any{"address": "0.0.0.0", "port_value": s.ListenerPort}},
			"filter_chains": []any{
				map[string]any{
					"filters": []any{
						map[string]any{
							"name": "envoy.filters.network.tcp_proxy",
							"typed_config": map[string]any{
								"@type": "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
								"stat_prefix": "tcp_" + s.Name,
								"cluster": "tcp_upstream_" + s.Name,
							},
						},
					},
				},
			},
		}
		listeners = append(listeners, l)

		cluster := map[string]any{
			"name": "tcp_upstream_" + s.Name,
			"connect_timeout": "2s",
			"type": "LOGICAL_DNS",
			"lb_policy": "ROUND_ROBIN",
			"load_assignment": map[string]any{
				"cluster_name": "tcp_upstream_" + s.Name,
				"endpoints": []any{
					map[string]any{"lb_endpoints": []any{map[string]any{"endpoint": map[string]any{"address": map[string]any{"socket_address": map[string]any{"address": s.UpstreamHost, "port_value": s.UpstreamPort}}}}}},
				},
			},
		}
		if s.TLS {
			cluster["transport_socket"] = map[string]any{
				"name": "envoy.transport_sockets.tls",
				"typed_config": map[string]any{"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext", "sni": s.UpstreamHost},
			}
		}
		clusters = append(clusters, cluster)
	}

	cfg := map[string]any{
		"static_resources": map[string]any{
			"listeners": listeners,
			"clusters":  clusters,
		},
		"admin": map[string]any{
			"access_log_path": "/tmp/admin_access.log",
			"address": map[string]any{"socket_address": map[string]any{"address": "127.0.0.1", "port_value": 9901}},
		},
	}
	out, err := syaml.Marshal(cfg)
	if err != nil { return "", err }
	return string(out), nil
}