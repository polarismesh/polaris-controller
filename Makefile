REGISTRY = ""
ORG = polarismesh
REPO = polaris-controller
SIDECAR_INIT_REPO = polaris-sidecar-init
ENVOY_SIDECAR_INIT_REPO = polaris-envoy-bootstrap-generator
IMAGE_TAG = v1.7.0-beta
PLATFORMS = linux/amd64,linux/arm64

.PHONY: all
all: build-amd64 build-arm64 build-multi-arch-image \
 	 build-sidecar-init build-envoy-sidecar-init push-image

.PHONY: build-amd64
build-amd64:
	@echo "------------------"
	@echo "--> Building binary for polaris-controller (linux/amd64)"
	@echo "------------------"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o ./bin/amd64/polaris-controller ./cmd/polaris-controller/main.go

.PHONY: build-arm64
build-arm64:
	@echo "------------------"
	@echo "--> Building binary for polaris-controller (linux/arm64)"
	@echo "------------------"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -a -o ./bin/arm64/polaris-controller ./cmd/polaris-controller/main.go

.PHONY: build-multi-arch-image
build-multi-arch-image:
	@echo "------------------"
	@echo "--> Building multi-arch docker image for polaris-controller"
	@echo "------------------"
	@docker buildx build --platform $(PLATFORMS) --tag $(ORG)/$(REPO):$(IMAGE_TAG) -f ./docker/Dockerfile --push ./

.PHONY: build-sidecar-init
build-sidecar-init:
	docker build ./sidecar/polaris-sidecar-init -f ./sidecar/polaris-sidecar-init/Dockerfile -t $(REGISTRY)$(ORG)/$(SIDECAR_INIT_REPO):$(IMAGE_TAG)

.PHONY: build-envoy-sidecar-init
build-envoy-sidecar-init:
	docker build ./sidecar/envoy-bootstrap-config-generator -f ./sidecar/envoy-bootstrap-config-generator/Dockerfile -t $(REGISTRY)$(ORG)/$(ENVOY_SIDECAR_INIT_REPO):$(IMAGE_TAG)

.PHONY: push-image
push-image:
	docker push $(REGISTRY)$(ORG)/$(SIDECAR_INIT_REPO):$(IMAGE_TAG)
	docker push $(REGISTRY)$(ORG)/$(ENVOY_SIDECAR_INIT_REPO):$(IMAGE_TAG)

.PHONY: clean
clean:
	rm -rf bin
	rm -rf polaris-controller-release*

