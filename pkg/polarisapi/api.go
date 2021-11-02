package polarisapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

var (
	PolarisHttpURL = "http://127.0.0.1:8080"
	PolarisGrpc    = "127.0.0.1:8090"
)

const (
	//getInstances    = "/naming/v1/instances"
	addInstances       = "/naming/v1/instances"
	deleteInstances    = "/naming/v1/instances/delete"
	getService         = "/naming/v1/services"
	updateService      = "/naming/v1/services"
	createService      = "/naming/v1/services"
	createServiceAlias = "/naming/v1/service/alias"
	getNamespace       = "/naming/v1/namespaces"
	createNamespace    = "/naming/v1/namespaces"
)

// GetAllInstances 获取全量Instances
//func GetAllInstances(namespace, service string, offset, limit int) (res Response, err error) {
//
//	var response Response
//	client := &http.Client{}
//	url := fmt.Sprintf("%s%s?namespace=%s&service=%s&offset=%d&limit=%d", PolarisHttpURL, getInstances,
//		namespace, service, offset, limit)
//	req, err := http.NewRequest(http.MethodGet, url, nil)
//	if err != nil {
//		klog.Errorf("Failed to build request %v", err)
//		return response, err
//	}
//
//	resp, err := client.Do(req)
//
//	if err != nil {
//		klog.Errorf("Failed to get request %v", err)
//		return response, err
//	}
//
//	defer resp.Body.Close()
//
//	body, err := ioutil.ReadAll(resp.Body)
//	if err != nil {
//		klog.Errorf("Failed to get request %v", err)
//		return response, err
//	}
//
//	err = json.Unmarshal(body, &response)
//
//	if err != nil {
//		klog.Errorf("Failed to unmarshal result %v", err)
//	}
//
//	return response, nil
//}

// AddInstances 平台增加实例接口
func AddInstances(instances []Instance, size int, msg string) (err error) {

	klog.Infof("Start to add all %s", msg)
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to add all %s (%v)", msg, time.Since(startTime))
	}()

	// 从这里开始拆分
	instanceArray := splitArray(instances, size)

	url := fmt.Sprintf("%s%s", PolarisHttpURL, addInstances)

	var wg sync.WaitGroup
	page := len(instanceArray)
	polarisErrors := NewPErrors()
	for i, v := range instanceArray {
		wg.Add(1)
		go func(i int, v []Instance) {
			defer wg.Done()
			requestID := uuid.New().String()
			requestByte, err := json.Marshal(v)

			if err != nil {
				klog.Errorf("Failed to marsh request %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg,
						i+1, page, err),
				})
				return
			}

			_, body, times, err :=
				polarisHttpRequest(requestID, http.MethodPost, url, requestByte)
			if err != nil {
				klog.Errorf("Failed request %s [%d/%d], err %v. (%s)", msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").
					Observe(times.Seconds())
				return
			}

			var response AddResponse
			err = json.Unmarshal(body, &response)
			klog.V(6).Infof("Send add %s [%d/%d], body is %s. (%s)", msg, i+1, page, string(requestByte), requestID)

			if err != nil {
				klog.Errorf("Failed unmarshal result %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.",
						msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").
					Observe(times.Seconds())
				return
			}

			metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", response.Info, fmt.Sprint(response.Code)).
				Observe(times.Seconds())

			dealAddInstanceResponse(response, msg, i, page, requestID, polarisErrors)

		}(i, v)
	}
	wg.Wait()

	return polarisErrors.GetError()
}

func dealAddInstanceResponse(response AddResponse, msg string,
	i int, page int, requestID string, polarisErrors *PErrors) {
	// 添加成功或者权限错误，都跳过
	if response.Code == 200000 {
		klog.Infof("Success add all %s [%d/%d], info %s. (%s)",
			msg, i+1, page, response.Info, requestID)
		return
	}

	if response.Code == 401000 {
		klog.Infof("Failed add all %s [%d/%d], info %s. (%s)",
			msg, i+1, page, response.Info, requestID)
		return
	}

	if len(response.Responses) != 0 {
		for _, ins := range response.Responses {
			// 400201 已存在
			// 200000 成功
			// 确认部分成功
			if ins.Code != 400201 && ins.Code != 200000 && ins.Code != 401000 {
				klog.Errorf("Failed add %s [%s:%d] [%d/%d], info %s. (%s)",
					msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
				polarisErrors.Append(PError{
					IP:     ins.Instance.Host,
					Port:   ins.Instance.Port,
					Weight: ins.Instance.Weight,
					Code:   util.Uint32Ptr(ins.Code),
					Info:   ins.Info,
				})
			} else {
				klog.Infof("Success add %s [%s:%d] [%d/%d], info %s. (%s)",
					msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
			}
		}
	} else {
		klog.Infof("Failed add %s all [%d/%d], info %s.", msg, i+1, page, response.Info)
		polarisErrors.Append(PError{
			Code: util.Uint32Ptr(500),
			Info: response.Info,
		})
	}
}

// DeleteInstances 平台删除实例接口
func DeleteInstances(instances []Instance, size int, msg string) (err error) {
	// 从这里开始拆分
	instanceArray := splitArray(instances, size)

	url := fmt.Sprintf("%s%s", PolarisHttpURL, deleteInstances)
	// 查看并发请求过程中，是否有失败的情况，只要存在，就应该放入队列中，重新同步
	var wg sync.WaitGroup
	page := len(instanceArray)
	polarisErrors := NewPErrors()
	for i, v := range instanceArray {
		wg.Add(1)
		go func(i int, v []Instance) {
			defer wg.Done()
			requestID := uuid.New().String()
			requestByte, err := json.Marshal(v)

			klog.Infof("Send delete %s [%d/%d], body is %s. (%s)",
				msg, i+1, page, string(requestByte), requestID)
			if err != nil {
				klog.Errorf("Failed to marsh request %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg,
						i+1, page, err),
				})
				return
			}
			var response AddResponse
			statusCode, body, times, err :=
				polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

			if err != nil {
				klog.Errorf("Failed to request %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").
					Observe(times.Seconds())
				return
			}
			if statusCode == http.StatusOK {
				klog.Infof("Success delete all %s [%d/%d], info %s. (%s)",
					msg, i+1, page, response.Info, requestID)
				// 删除成功就没有返回值了，所以默认指定为2000
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Success", "2000").
					Observe(times.Seconds())
				return
			}
			err = json.Unmarshal(body, &response)
			klog.V(6).Infof("%s [%d/%d], body is %s. (%s)",
				msg, i+1, page, string(body), requestID)

			if err != nil {
				klog.Errorf("Failed unmarshal result %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").Observe(times.Seconds())
				return
			}
			dealDeleteInstanceResponse(response, msg, i, page, requestID, times, polarisErrors)
		}(i, v)
	}
	wg.Wait()
	return polarisErrors.GetError()
}

func dealDeleteInstanceResponse(response AddResponse, msg string, i int, page int, requestID string,
	times time.Duration, polarisErrors *PErrors) {
	// 如果是成功或者未授权
	if response.Code == 200000 {
		klog.Infof("Success delete all %s [%d/%d], info %s. (%s)",
			msg, i+1, page, response.Info, requestID)
		metrics.InstanceRequestSync.WithLabelValues(
			"Add", "Platform", response.Info, fmt.Sprint(response.Code)).Observe(times.Seconds())
		return
	}
	if response.Code == 401000 {
		klog.Infof("Failed delete all %s [%d/%d], info %s. (%s)",
			msg, i+1, page, response.Info, requestID)
		metrics.InstanceRequestSync.WithLabelValues(
			"Add", "Platform", response.Info, fmt.Sprint(response.Code)).Observe(times.Seconds())
		return
	}

	// 如果response不等于空，要具体判断是否有正常处理成功的instances
	if len(response.Responses) != 0 {
		for _, ins := range response.Responses {
			// 200000 删除成功
			if ins.Code != 200000 && ins.Code != 401000 {
				klog.Errorf("Failed delete %s [%s:%d] [%d/%d], info %s. (%s)",
					msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
				polarisErrors.Append(PError{
					IP:     ins.Instance.Host,
					Port:   ins.Instance.Port,
					Weight: ins.Instance.Weight,
					Code:   util.Uint32Ptr(ins.Code),
					Info:   ins.Info,
				})
			} else {
				klog.Infof("Success delete %s [%s:%d] [%d/%d], info %s. (%s)",
					msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
			}
		}
	} else {
		klog.Infof("Failed delete %s all [%d/%d], info %s.", msg, i+1, page, response.Info)
		polarisErrors.Append(PError{
			Code: util.Uint32Ptr(500),
			Info: response.Info,
		})
	}
	metrics.InstanceRequestSync.WithLabelValues(
		"Add", "Platform", response.Info, fmt.Sprint(response.Code)).Observe(times.Seconds())
}

// UpdateInstances 修改平台实例接口
func UpdateInstances(instances []Instance, size int, msg string) (err error) {

	klog.Infof("Start to add all %s", msg)
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to add all %s (%v)", msg, time.Since(startTime))
	}()

	// 从这里开始拆分
	instanceArray := splitArray(instances, size)
	url := fmt.Sprintf("%s%s", PolarisHttpURL, addInstances)

	var wg sync.WaitGroup
	page := len(instanceArray)

	polarisErrors := NewPErrors()
	for i, v := range instanceArray {
		wg.Add(1)
		go func(i int, v []Instance) {
			defer wg.Done()
			requestID := uuid.New().String()
			requestByte, err := json.Marshal(v)
			if err != nil {
				klog.Errorf("Failed to marsh request %s] [%d/%d], err %v.",
					msg, i+1, page, err)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg,
						i+1, page, err),
				})
				return
			}
			klog.Infof("Update msg body is %s", string(requestByte))
			httpCode, body, _, err := polarisHttpRequest(requestID, http.MethodPut, url, requestByte)
			if err != nil {
				klog.Errorf("Failed request %s [%d/%d], err %v. requestId: %s", msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				return
			}

			if httpCode == http.StatusOK {
				return
			}

			var response AddResponse
			err = json.Unmarshal(body, &response)
			if err != nil {
				klog.Errorf("Failed unmarshal result %s [%d/%d], err %v.",
					msg, i+1, page, err)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.",
						msg, i+1, page, err),
				})
				return
			}
			klog.Errorf("update response is %v", response)
			dealUpdateInstanceResponse(response, msg, i, page, polarisErrors)
		}(i, v)
	}
	wg.Wait()
	return polarisErrors.GetError()
}

func dealUpdateInstanceResponse(response AddResponse, msg string,
	i int, page int, polarisErrors *PErrors) {
	// 添加成功或者权限错误，都跳过
	if response.Code == 200000 {
		klog.Infof("Success add all %s [%d/%d], info %s.", msg, i+1, page, response.Info)
		return
	}

	if len(response.Responses) != 0 {
		for _, ins := range response.Responses {
			// 200002 update data is no change, no need to update
			// 200000 execute success
			if ins.Code != 200002 && ins.Code != 200000 {
				klog.Errorf("Failed add %s [%s/%s] [%s:%d] [%d/%d], info %s.",
					msg, ins.Instance.Namespace, ins.Instance.Service,
					ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info)
				polarisErrors.Append(PError{
					IP:     ins.Instance.Host,
					Port:   ins.Instance.Port,
					Weight: ins.Instance.Weight,
					Code:   util.Uint32Ptr(ins.Code),
					Info:   ins.Info,
				})
			} else {
				klog.Infof("Success add %s [%s/%s] [%s:%d] [%d/%d], info %s.",
					msg, ins.Instance.Namespace, ins.Instance.Service,
					ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info)
			}
		}
	} else {
		klog.Infof("Failed update %s all [%d/%d], info %s.", msg, i+1, page, response.Info)
		polarisErrors.Append(PError{
			Code: util.Uint32Ptr(500),
			Info: response.Info,
		})
	}
}

// GetService 查询服务接口
// GET /naming/v1/services?参数名=参数值
func GetService(service *v1.Service) (res GetServiceResponse, err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())

	klog.Infof("Start to get %s", serviceMsg)
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to get %s (%v)", serviceMsg, time.Since(startTime))
	}()
	var response GetServiceResponse
	requestID := uuid.New().String()

	polarisNamespace := service.Namespace
	if polarisNamespace == "" {
		return response, fmt.Errorf("failed service is invalid, polarisNamespace is empty")
	}
	polaris := service.Name
	if polaris == "" {
		return response, fmt.Errorf("failed service is invalid, polarisService is empty")
	}

	url := fmt.Sprintf("%s%s?namespace=%s&name=%s&offset=%d&limit=%d",
		PolarisHttpURL, getService, polarisNamespace, polaris, 0, 100)

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodGet, url, nil)

	klog.Infof("Get service %s, body %s", serviceMsg, string(body))

	if err != nil {
		klog.Errorf("Failed to get request %s %v", serviceMsg, err)
		return response, err
	}

	err = json.Unmarshal(body, &response)
	if err != nil {
		klog.Errorf("Failed to unmarshal result %s %v", serviceMsg, err)
		return response, err
	}

	if statusCode != http.StatusOK {
		klog.Errorf("Failed to get service %s %s", serviceMsg, response.Info)
		return response, fmt.Errorf("failed to get service %s %s", serviceMsg, response.Info)
	}

	return response, nil
}

// ListService 查询服务列表
// GET /naming/v1/services?keys=platform&values=tke
func ListService(clusterID string) (res GetServiceResponse, err error) {

	klog.Info("Start to get platform service list")
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to get platform service list (%v)", time.Since(startTime))
	}()
	var response GetServiceResponse
	requestID := uuid.New().String()

	url := fmt.Sprintf("%s%s?keys=platform&values=tke&offset=%d&limit=%d",
		PolarisHttpURL, getService, 0, 100)

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodGet, url, nil)

	if err != nil {
		klog.Errorf("Failed to get platform service list %v", err)
		return response, err
	}

	err = json.Unmarshal(body, &response)
	if err != nil {
		klog.Errorf("Failed to unmarshal platform service list %v", err)
		return response, err
	}

	if statusCode != http.StatusOK {
		klog.Errorf("Failed to get platform service list %s", response.Info)
		return response, fmt.Errorf("failed to get platform service list %s", response.Info)
	}

	return response, nil
}

func getPolarisPorts(service *v1.Service) string {

	var ports []string
	for _, port := range service.Spec.Ports {
		ports = append(ports, strconv.Itoa(int(port.Port)))
	}

	return strings.Join(ports, ",")
}

// CreateService 创建北极星服务
func CreateService(service *v1.Service) (CreateServicesResponse, error) {

	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())

	klog.Infof("Start to create service [%s][%s]", service.Namespace, service.Name)
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to update %s (%v)", serviceMsg, time.Since(startTime))
	}()

	var response CreateServicesResponse
	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, createService)

	createRequest := []CreateServiceRequest{
		{
			Name:      service.Name,
			Namespace: service.Namespace,
			Owners:    Platform,
			Ports:     getPolarisPorts(service),
		},
	}

	requestByte, err := json.Marshal(createRequest)
	if err != nil {
		klog.Errorf("Failed to marsh request %s %v", serviceMsg, err)
		return response, err
	}

	klog.Infof("create service %s, body %s", serviceMsg, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

	if err != nil {
		klog.Errorf("Failed to create service %s %v", serviceMsg, err)
		return response, err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			klog.Errorf("Failed to unmarshal result %s, %v, %s", serviceMsg, err, string(body))
			return CreateServicesResponse{}, err
		}
		if response.Code != ExistedResource {
			klog.Errorf("Failed to create service %s %v", serviceMsg, response.Info)
			return response, fmt.Errorf("create namespace failed: " + response.Info)
		}
	}

	return response, nil
}

// CreateServiceAlias 创建北极星服务别名
func CreateServiceAlias(service *v1.Service) (CreateServiceAliasResponse, error) {

	alias := service.GetAnnotations()[util.PolarisAliasNamespace]
	aliasNs := service.GetAnnotations()[util.PolarisAliasService]

	serviceAliasMsg := fmt.Sprintf("[%s/%s], [%s/%s]", service.GetNamespace(), service.GetName(), aliasNs, alias)

	klog.Infof("Start to create service alias %s", serviceAliasMsg)
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to create service alias %s (%v)", serviceAliasMsg, time.Since(startTime))
	}()

	var response CreateServiceAliasResponse
	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, createServiceAlias)

	createRequest := &CreateServiceAliasRequest{
		Service:        service.Name,
		Namespace:      service.Namespace,
		Alias:          alias,
		AliasNamespace: aliasNs,
		Owners:         Platform,
	}

	requestByte, err := json.Marshal(createRequest)
	if err != nil {
		klog.Errorf("Failed to marsh request %s %v", serviceAliasMsg, err)
		return response, err
	}

	klog.Infof("create service alias %s, body %s", serviceAliasMsg, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

	if err != nil {
		klog.Errorf("Failed to create service alias %s %v", serviceAliasMsg, err)
		return response, err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			klog.Errorf("Failed to unmarshal result %s, %v, %s", serviceAliasMsg, err, string(body))
			return CreateServiceAliasResponse{}, err
		}
		if response.Code != ExistedResource {
			klog.Errorf("Failed to create service alias %s %v", serviceAliasMsg, response.Info)
			return response, fmt.Errorf("create service alias failed: " + response.Info)
		}
	}

	return response, nil
}

// UpdateService 更新服务字段
// PUT /naming/v1/services
func UpdateService(service *v1.Service, request []Service) (int, PutServicesResponse, error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	klog.Infof("Start to update %s", serviceMsg)
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to update %s (%v)", serviceMsg, time.Since(startTime))
	}()

	var response PutServicesResponse
	requestID := uuid.New().String()

	url := fmt.Sprintf("%s%s", PolarisHttpURL, updateService)

	requestByte, err := json.Marshal(request)
	if err != nil {
		klog.Errorf("Failed to marsh request %s %v", serviceMsg, err)
		return 0, PutServicesResponse{}, err
	}

	klog.Infof("Put service %s, body %s", serviceMsg, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPut, url, requestByte)

	if err != nil {
		klog.Errorf("Failed to get request %s %v", serviceMsg, err)
		return statusCode, PutServicesResponse{}, err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			klog.Errorf("Failed to unmarshal result %s, %v, %s", serviceMsg, err, string(body))
			return statusCode, PutServicesResponse{}, err
		}
		klog.Errorf("Failed to update result %s %v", serviceMsg, response.Info)
		return statusCode, response, fmt.Errorf("Put service failed: " + response.Info)
	}

	return statusCode, response, nil
}

// CreateNamespaces 创建北极星命名空间
func CreateNamespaces(namespace string) (CreateNamespacesResponse, error) {
	klog.Info("Start to create namespace ", namespace)
	startTime := time.Now()
	defer func() {
		klog.Infof("Finish to create namespace %s (%v)", namespace, time.Since(startTime))
	}()

	var response CreateNamespacesResponse
	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, createNamespace)

	createRequest := []CreateNamespacesRequest{
		{
			Name:   namespace,
			Owners: Platform,
		},
	}

	requestByte, err := json.Marshal(createRequest)
	if err != nil {
		klog.Errorf("Failed to marsh request %s %v", namespace, err)
		return response, err
	}

	klog.Infof("create namespace %s, body %s", namespace, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

	if err != nil {
		klog.Errorf("Failed to get result %s %v", namespace, err)
		return response, err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			klog.Errorf("Failed to unmarshal result %s, %v, %s", namespace, err, string(body))
			return CreateNamespacesResponse{}, err
		}
		if response.Responses == nil || len(response.Responses) == 0 ||
			response.Responses[0].Code != ExistedResource {
			klog.Errorf("Failed to create namespace %s ,error response: %v", namespace, response)
			return response, fmt.Errorf("create namespace failed: " + response.Info)
		}
	}

	return response, nil
}

func splitArray(instances []Instance, size int) [][]Instance {

	size64 := float64(size)
	length := len(instances)
	page64 := math.Ceil(float64(length) / size64)
	page := int(page64)

	klog.Infof("Current instance,size/page/total [%d/%d/%d]", length, size, page)
	var instanceArray [][]Instance

	for i := 0; i < page; i++ {
		start := i * size
		end := start + size
		if i == page-1 {
			end = length
		}
		instanceArray = append(instanceArray, instances[start:end])
	}

	return instanceArray
}

// polarisHttpRequest
func polarisHttpRequest(
	requestID string, method string,
	url string, requestByte []byte) (int, []byte, time.Duration, error) {

	startTime := time.Now()
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(requestByte))
	if err != nil {
		klog.Errorf("Failed to set request %v", err)
		return 0, nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Request-Id", requestID)

	resp, err := client.Do(req)

	if err != nil {
		klog.Errorf("Failed to get request %v", err)
		return 0, nil, time.Since(startTime), err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		klog.Errorf("Failed to get request %v", err)
		return 0, nil, time.Since(startTime), err
	}

	return resp.StatusCode, body, time.Since(startTime), err
}
