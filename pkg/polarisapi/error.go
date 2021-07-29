package polarisapi

import (
	"encoding/json"
	"fmt"
	"sync"
)

// NewPErrors 实例化新的errors
func NewPErrors() *PErrors {
	return &PErrors{}
}

// PErrors API 批量接口错误数据
type PErrors struct {
	m sync.Mutex
	e []PError
}

// PError API 错误数据
type PError struct {
	ID      string  `json:"id,omitempty"`
	PodName string  `json:"podName,omitempty"`
	Weight  *int    `json:"weight,omitempty"`
	Port    *int    `json:"port,omitempty"`
	IP      string  `json:"ip,omitempty"`
	Code    *uint32 `json:"code,omitempty"`
	Info    string  `json:"info,omitempty"`
}

// Append 增加errors方法
func (pe *PErrors) Append(pError PError) {
	pe.m.Lock()
	defer pe.m.Unlock()
	pe.e = append(pe.e, pError)
}

// GetError 获取error错误
func (pe *PErrors) GetError() error {
	pe.m.Lock()
	defer pe.m.Unlock()

	if len(pe.e) != 0 {
		pErrorString, _ := json.Marshal(pe.e)
		return fmt.Errorf("%s", pErrorString)
	}
	return nil
}
