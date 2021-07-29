.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o ./bin/polaris-controller ./cmd/polaris-controller/main.go