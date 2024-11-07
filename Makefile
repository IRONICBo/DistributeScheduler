build:
	KO_DOCKER_REPO=docker.io/asklv ko build

fmt:
	go fmt ./...

.PHONY: build