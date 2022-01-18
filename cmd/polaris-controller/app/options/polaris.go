package options

import (
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolarisControllerOptions
type PolarisControllerOptions struct {
	*PolarisControllerConfiguration
}

// PolarisControllerConfiguration holds configuration for a polaris controller
type PolarisControllerConfiguration struct {
	// port is the port that the controller-manager's http service runs on.
	ClusterName            string
	ConcurrentPolarisSyncs int
	Size                   int
	MinAccountingPeriod    metav1.Duration
	SyncMode               string
}

// AddFlags adds flags related to generic for controller manager to the specified FlagSet.
func (o *PolarisControllerOptions) AddFlags(fs *pflag.FlagSet) {
	if o == nil {
		return
	}
	fs.StringVar(&o.ClusterName, "cluster-name", "", "clusterName")
	fs.IntVar(&o.ConcurrentPolarisSyncs, "concurrent-polaris-syncs", 5, "service queue workers")
	fs.IntVar(&o.Size, "concurrency-polaris-size", 100, "polaris request size pre time")
	fs.DurationVar(
		&o.MinAccountingPeriod.Duration, "min-accounting-period",
		o.MinAccountingPeriod.Duration,
		"The resync period in reflectors will be random between MinResyncPeriod and 2*MinResyncPeriod.")
	fs.StringVar(&o.SyncMode, "sync-mode", "ALL", "polaris-controller sync mode, supports 'ALL' , 'NAMESPACE'")
}

// ApplyTo fills up generic config with options.
func (o *PolarisControllerOptions) ApplyTo(cfg *PolarisControllerConfiguration) error {
	if o == nil {
		return nil
	}

	cfg.ClusterName = o.ClusterName
	cfg.ConcurrentPolarisSyncs = o.ConcurrentPolarisSyncs
	cfg.Size = o.Size
	cfg.MinAccountingPeriod = o.MinAccountingPeriod

	return nil
}
