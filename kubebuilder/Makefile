IMG ?= universal-proxy-controller:dev

.PHONY: build
build:
	GO111MODULE=on go build ./...

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push:
	docker push $(IMG)

.PHONY: deploy
deploy:
	kubectl apply -f config/

.PHONY: undeploy
undeploy:
	kubectl delete -f config/ --ignore-not-found