IMAGE     ?= alfaos/alfad
VERSION   ?= dev
PLATFORMS ?= linux/amd64,linux/arm64,linux/arm/v7
COMPOSE   := docker compose -f deploy/docker-compose.yml

.PHONY: tidy build test vet image image-multiarch up up-nvidia down logs setup-token

tidy:
	cd backend && go mod tidy

build:
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/alfad ./cmd/alfad

test:
	cd backend && go test ./...

vet:
	cd backend && go vet ./...

# Single-arch image loaded into the local engine.
image:
	docker buildx build --load --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) backend

# All target architectures; multi-platform images must be pushed to a registry.
image-multiarch:
	docker buildx build --push --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) backend

up:
	$(COMPOSE) up -d --build

up-nvidia:
	$(COMPOSE) -f deploy/docker-compose.nvidia.yml up -d --build

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f alfad

setup-token:
	@$(COMPOSE) logs alfad | grep -o '"setup_token":"[^"]*"' | tail -1
