module github.com/polarismesh/polaris-controller

go 1.14

require (
	contrib.go.opencensus.io/exporter/prometheus v0.1.0
	github.com/cncf/udpa/go v0.0.0-20191209042840-269d4d468f6f
	github.com/davecgh/go-spew v1.1.1
	github.com/envoyproxy/go-control-plane v0.9.4
	github.com/fsnotify/fsnotify v1.4.7
	github.com/ghodss/yaml v1.0.0
	github.com/gogo/protobuf v1.3.0
	github.com/golang/protobuf v1.4.2
	github.com/google/go-cmp v0.4.0
	github.com/google/uuid v1.2.0
	github.com/hashicorp/go-multierror v1.0.0
	github.com/howeyc/fsnotify v0.9.0
	github.com/mitchellh/copystructure v1.0.0
	github.com/natefinch/lumberjack v2.0.0+incompatible
	github.com/openshift/api v3.9.1-0.20191008181517-e4fd21196097+incompatible
	github.com/polarismesh/polaris-go v1.0.0
	github.com/prometheus/client_golang v1.1.0
	github.com/spaolacci/murmur3 v1.1.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	go.opencensus.io v0.22.2
	go.uber.org/zap v1.10.0
	golang.org/x/sys v0.0.0-20191026070338-33540a1f6037 // indirect
	google.golang.org/grpc v1.26.0
	gopkg.in/yaml.v2 v2.4.0
	// istio.io/istio v0.0.0-20200812220246-25bea56c0eb0
	// istio.io/pkg v0.0.0-20200324191837-25e6bb9cf135
	k8s.io/api v0.17.2
	k8s.io/apiextensions-apiserver v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/apiserver v0.17.2
	k8s.io/client-go v0.17.2
	k8s.io/component-base v0.17.2
	k8s.io/klog v1.0.0
)

replace github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.8.1
