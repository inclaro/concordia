.PHONY: build deploy test

build:
	./scripts/build.sh

deploy: build
	./scripts/deploy.sh

test:
	go test ./app/...
