.PHONY: build

REGISTRY = docker.io
REPO = polarismesh/polaris-controller
SIDECAR_INIT_REPO = polarismesh/polaris-sidecar-init
IMAGE_TAG = v1.0.0

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o ./bin/polaris-controller ./cmd/polaris-controller/main.go

build-image:
	docker build . -f ./docker/Dockerfile -t $(REGISTRY)/$(REPO):$(IMAGE_TAG)

build-sidecar-init:
	docker build ./sidecar/polaris-sidecar-init -f ./sidecar/polaris-sidecar-init/Dockerfile -t $(REGISTRY)/$(SIDECAR_INIT_REPO):$(IMAGE_TAG)

push-image: build build-image build-sidecar-init login
	docker push $(REGISTRY)/$(REPO):$(IMAGE_TAG)
	docker push $(REGISTRY)/$(SIDECAR_INIT_REPO):$(IMAGE_TAG)

login:
	@docker login --username=$(DOCKER_USER) --password=$(DOCKER_PASS) $(REGISTRY)