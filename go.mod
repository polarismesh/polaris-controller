module github.com/polarismesh/polaris-controller

go 1.14

require (
	github.com/ghodss/yaml v1.0.0
	github.com/gogo/protobuf v1.3.0
	github.com/golang/groupcache v0.0.0-20190702054246-869f871628b6 // indirect
	github.com/google/uuid v1.2.0
	github.com/hashicorp/go-multierror v1.0.0
	github.com/howeyc/fsnotify v0.9.0
	github.com/openshift/api v3.9.1-0.20191008181517-e4fd21196097+incompatible
	github.com/polarismesh/polaris-go v1.0.0
	github.com/prometheus/client_golang v1.1.0 // indirect
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	golang.org/x/sys v0.0.0-20191026070338-33540a1f6037 // indirect
	google.golang.org/grpc v1.26.0
	gopkg.in/yaml.v2 v2.4.0
	//istio.io/istio v0.0.0-20200812220246-25bea56c0eb0
	//istio.io/pkg v0.0.0-20200324191837-25e6bb9cf135
	k8s.io/api v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/apiserver v0.17.2
	k8s.io/client-go v0.17.2
	k8s.io/component-base v0.17.2
	k8s.io/klog v1.0.0
)

replace github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.8.1
