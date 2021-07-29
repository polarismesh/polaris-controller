module github.com/polarismesh/polaris-controller

go 1.14

require (
	git.code.oa.com/polaris/polaris-go v0.8.4
	github.com/google/uuid v1.2.0
	github.com/hashicorp/go-multierror v1.0.0 // indirect
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	google.golang.org/grpc v1.26.0
	gopkg.in/yaml.v2 v2.4.0 // indirect
	istio.io/istio v0.0.0-20200812220246-25bea56c0eb0
	istio.io/pkg v0.0.0-20200324191837-25e6bb9cf135
	k8s.io/api v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/apiserver v0.17.2
	k8s.io/client-go v0.17.2
	k8s.io/component-base v0.17.2
	k8s.io/klog v1.0.0
)

replace github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.8.1