REGISTRY = ""
REPO = polarismesh/polaris-controller
SIDECAR_INIT_REPO = polarismesh/polaris-sidecar-init
ENVOY_SIDECAR_INIT_REPO = polarismesh/polaris-envoy-bootstrap-generator
IMAGE_TAG = v1.2.2
PLATFORMS = linux/amd64,linux/arm64

IS_LOGIN := $(shell jq '.auths | length' $(HOME)/.docker/config.json)
HAS_BUILDER := $(shell docker buildx ls | grep -q  polaris-controller-builder; echo $$?)

.PHONY: all
all: init-builder build-amd64 build-arm64 build-multi-arch-image \
 	 build-sidecar-init build-envoy-sidecar-init \
 	 login push-image

.PHONY: all-nologin
all-nologin: build-amd64 build-arm64 build-multi-arch-image \
 	 build-sidecar-init build-envoy-sidecar-init \
 	 push-image

.PHONY: init-builder
init-builder:
	@echo "------------------"
	@echo "--> Create a builder instance (if not created yet) and switch the build environment to the newly created builder instance"
	@echo "------------------"
	@if [ $(HAS_BUILDER) -ne 0 ]; then \
      	docker buildx create --name polaris-controller-builder --use; \
    fi

.PHONY: build-amd64
build-amd64:
	@echo "------------------"
	@echo "--> Building binary for polaris-controller (amd64)"
	@echo "------------------"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o ./bin/amd64/polaris-controller ./cmd/polaris-controller/main.go

.PHONY: build-arm64
build-arm64:
	@echo "------------------"
	@echo "--> Building binary for polaris-controller (arm64)"
	@echo "------------------"
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -a -o ./bin/arm64/polaris-controller ./cmd/polaris-controller/main.go

.PHONY: build-multi-arch-image
build-multi-arch-image:
	@echo "------------------"
	@echo "--> Building multi-arch docker image for polaris-controller"
	@echo "------------------"
	@docker buildx build \
		--platform $(PLATFORMS) \
		--tag $(REPO):$(IMAGE_TAG) \
		--load \
		-f ./docker/Dockerfile \
		.

.PHONY: build-sidecar-init
build-sidecar-init:
	docker build ./sidecar/polaris-sidecar-init -f ./sidecar/polaris-sidecar-init/Dockerfile -t $(REGISTRY)$(SIDECAR_INIT_REPO):$(IMAGE_TAG)

.PHONY: build-envoy-sidecar-init
build-envoy-sidecar-init:
	docker build ./sidecar/envoy-bootstrap-config-generator -f ./sidecar/envoy-bootstrap-config-generator/Dockerfile -t $(REGISTRY)$(ENVOY_SIDECAR_INIT_REPO):$(IMAGE_TAG)

.PHONY: login
login:
	@if [ $(IS_LOGIN) -eq 0 ]; then \
		@docker login --username=$(DOCKER_USER) --password=$(DOCKER_PASS) $(REGISTRY)
	fi

.PHONY: push-image
push-image:
	docker push $(REGISTRY)$(REPO):$(IMAGE_TAG)
	docker push $(REGISTRY)$(SIDECAR_INIT_REPO):$(IMAGE_TAG)
	docker push $(REGISTRY)$(ENVOY_SIDECAR_INIT_REPO):$(IMAGE_TAG)
