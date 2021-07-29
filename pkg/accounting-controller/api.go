package accountingcontroller

import (
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"k8s.io/klog"
)

// listService 获取北极星的包含本集群的服务列表
func (p *PolarisAccountController) listService() {
	response, err := polarisapi.ListService(p.config.PolarisController.ClusterName)
	if err != nil {
		klog.Errorf("err %v", err)
	}
	if response.Code != 200000 {
		klog.Errorf("response %v", response)
	}
	if len(response.Services) < 1 {
		klog.Errorf("response %v", response)
	}
}
