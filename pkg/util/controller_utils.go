package util

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"strings"
)

var (
	keyFunc = cache.DeletionHandlingMetaNamespaceKeyFunc
)

func GenObjectQueueKey(obj interface{}) (string, error) {
	key, err := keyFunc(obj)
	if err != nil {
		return "", err
	}

	return key, err
}

// GenServiceQueueKeyWithFlag 在 namespace 的事件流程中使用。
// 产生 service queue 中的 key，flag 表示添加时是否是北极星的服务
func GenServiceQueueKeyWithFlag(svc *v1.Service, flag string) (string, error) {
	key, err := keyFunc(svc)
	if err != nil {
		return "", err
	}
	key += "~" + flag

	return key, nil
}

// GenServiceQueueKey 产生 service 中 queue 中用的 key
func GenServiceQueueKey(svc *v1.Service) (string, error) {
	key, err := keyFunc(svc)
	if err != nil {
		return "", err
	}

	return key, nil
}

// GetServiceRealKeyWithFlag 从 service queue 中的 key ，解析出 namespace、service、flag
func GetServiceRealKeyWithFlag(queueKey string) (string, string, string, error) {
	if queueKey == "" {
		return "", "", "", nil
	}
	op := ""
	ss := strings.Split(queueKey, "~")
	namespace, service, err := cache.SplitMetaNamespaceKey(ss[0])
	if err != nil {
		return "", "", "", err
	}
	if len(ss) != 1 {
		op = ss[1]
	}
	return namespace, service, op, nil
}
