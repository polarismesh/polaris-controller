IMAGE_TAG ?= v2.1.0
ORG ?= polarismesh
REPO = polaris-controller
SIDECAR_INIT_REPO = polaris-sidecar-init
ENVOY_SIDECAR_INIT_REPO = polaris-envoy-bootstrap-generator
PLATFORMS = linux/amd64,linux/arm64

.PHONY: all
all: push-all-image

.PHONY: push-all-image
push-all-image: push-controller-image push-init-image

.PHONY: gen-all-image
gen-all-image: gen-controller-image gen-init-image

.PHONY: clean
clean:
	rm -rf bin
	rm -rf polaris-controller-release*

.PHONY: fmt
fmt:  ## Run go fmt against code.
	go fmt ./...

.PHONY: build-amd64
build-amd64: clean fmt
	@echo "------------------"
	@echo "--> Building binary for polaris-controller (linux/amd64)"
	@echo "------------------"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o ./bin/amd64/polaris-controller ./cmd/polaris-controller/main.go

.PHONY: build-arm64
build-arm64: clean fmt
	@echo "------------------"
	@echo "--> Building binary for polaris-controller (linux/arm64)"
	@echo "------------------"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -a -o ./bin/arm64/polaris-controller ./cmd/polaris-controller/main.go

.PHONY: bin
bin: build-amd64 build-arm64
	@echo "------------------"
	@echo "--> Building binary for polaris-controller"
	@echo "------------------"

.PHONY: gen-controller-image
gen-controller-image: bin
	@echo "------------------"
	@echo "--> Generate multi-arch docker image to registry for polaris-controller"
	@echo "------------------"
	@docker buildx build ./ --file ./docker/Dockerfile --tag $(ORG)/$(REPO):$(IMAGE_TAG) --platform $(PLATFORMS)

.PHONY: push-controller-image
push-controller-image: bin
	@echo "------------------"
	@echo "--> Building and push multi-arch docker image for polaris-controller"
	@echo "------------------"
	@docker buildx build ./ --file ./docker/Dockerfile --tag $(ORG)/$(REPO):$(IMAGE_TAG) --platform $(PLATFORMS) --push

.PHONY: gen-init-image
gen-init-image:
	@echo "------------------"
	@echo "--> Building multi-arch docker image for polaris-sidecar-init"
	@echo "------------------"
	@docker buildx build ./sidecar/polaris-sidecar-init --file ./sidecar/polaris-sidecar-init/Dockerfile --tag $(ORG)/$(SIDECAR_INIT_REPO):$(IMAGE_TAG) --platform $(PLATFORMS)
	@echo "------------------"
	@echo "--> Building multi-arch docker image for envoy-bootstrap-config-generator"
	@echo "------------------"
	@docker buildx build ./sidecar/envoy-bootstrap-config-generator --file ./sidecar/envoy-bootstrap-config-generator/Dockerfile --tag $(ORG)/$(ENVOY_SIDECAR_INIT_REPO):$(IMAGE_TAG) --platform $(PLATFORMS)

.PHONY: push-init-image
push-init-image:
	@echo "------------------"
	@echo "--> Building and push multi-arch docker image for polaris-sidecar-init"
	@echo "------------------"
	@docker buildx build ./sidecar/polaris-sidecar-init --file ./sidecar/polaris-sidecar-init/Dockerfile --tag $(ORG)/$(SIDECAR_INIT_REPO):$(IMAGE_TAG) --platform $(PLATFORMS) --push
	@echo "------------------"
	@echo "--> Building and push multi-arch docker image for envoy-bootstrap-config-generator"
	@echo "------------------"
	@docker buildx build ./sidecar/envoy-bootstrap-config-generator --file ./sidecar/envoy-bootstrap-config-generator/Dockerfile --tag $(ORG)/$(ENVOY_SIDECAR_INIT_REPO):$(IMAGE_TAG) --platform $(PLATFORMS) --push
