package common

import "os"

var (
	PolarisServerAddress       string = "127.0.0.1"
	PolarisServerGrpcAddress   string = "127.0.0.1:8091"
	PolarisControllerNamespace string = os.Getenv("POD_NAMESPACE")
)
