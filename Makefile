.PHONY: run build image push vendor client-gen clean

dockerhubUser = xxx/xxx
tag = latest
app = csi-3fs
targetDir ?= bin
buildx ?= false
dualPlatform ?= linux/amd64,linux/arm64

# build all apps
build:
	@echo "Building $(app)"
	CGO_ENABLED=0 go build -ldflags "-s -w" -o $(targetDir)/$(app) ./cmd/csi-driver-3fs

# build all images
image:
ifeq ($(buildx), false)
	echo "Building $(app) image"
	docker build -t $(dockerhubUser)/$(app):$(tag) --no-cache --build-arg APP=$(app) .
else ifeq ($(buildx), true)
	echo "Building $(app) multi-arch image"
	docker buildx build -t $(dockerhubUser)/$(app):$(tag) --no-cache --platform $(dualPlatform) --push --build-arg APP=$(app) .
endif

# push all images
push: image
	echo "Pushing $(app) image"
	docker push $(dockerhubUser)/$(app):$(tag)

PHONY: golang-vet
# golang vet
golang-vet:
	go vet ./...

.PHONY: golang-fmt
# golang code formatting
golang-fmt:
	golines --ignore-generated --ignored-dirs=vendor -w --max-len=150 --base-formatter=gofumpt .
	gci write -s standard -s default -s 'prefix(github.com/MooreThreads/csi-driver-3fs)' --skip-generated --skip-vendor .

# show help
help:
	@echo ''
	@echo 'Usage:'
	@echo ' make [target]'
	@echo ''
	@echo 'Targets:'
	@awk '/^[a-zA-Z\-0-9]+:/ { \
	helpMessage = match(lastLine, /^# (.*)/); \
		if (helpMessage) { \
			helpCommand = substr($$1, 0, index($$1, ":")-1); \
			helpMessage = substr(lastLine, RSTART + 2, RLENGTH); \
			printf "\033[36m%-22s\033[0m %s\n", helpCommand,helpMessage; \
		} \
	} \
	{ lastLine = $$0 }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help