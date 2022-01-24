.PHONY: build

REGISTRY = ccr.ccs.tencentyun.com
REPO = polaris_mesh_test/polaris-controller
IMAGE_TAG = v1.0.0

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o ./bin/polaris-controller ./cmd/polaris-controller/main.go

build-image:
	docker build . -f ./docker/Dockerfile -t $(REGISTRY)/$(REPO):$(IMAGE_TAG)

push-image: build build-image login
	docker push $(REGISTRY)/$(REPO):$(IMAGE_TAG)

push-image-dockerhub: build build-image login
	docker push $(REPO):$(IMAGE_TAG)

login:
	@docker login --username=$(DOCKER_USER) --password=$(DOCKER_PASS)
