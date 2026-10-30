SHELL := /bin/bash
COMPOSE := docker compose
.DEFAULT_GOAL := help

.PHONY: help env up down logs verify reset-demo load chaos test test-integration cover lint openapi

help: ## Komutları listeler
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

env:
	@test -f .env || cp .env.example .env

up: env ## Tüm sistemi derleyip başlatır ve sağlıklı olana kadar bekler
	$(COMPOSE) up -d --build --wait

down: ## Sistemi durdurur (veri korunur)
	$(COMPOSE) down

logs: ## Logları izler
	$(COMPOSE) logs -f --tail=100

verify: up ## Uçtan uca doğrulama: her F-/T- kimliği için PASS/FAIL tablosu
	$(COMPOSE) --profile verify run --rm --build verify

reset-demo: env ## Verileri ilk seed haline döndürür (hacmi silip yeniden kurar)
	$(COMPOSE) down -v --remove-orphans
	$(COMPOSE) up -d --build --wait

load: up ## k6 yük testi (README'deki tablo bu komutla üretilir)
	docker run --rm -i --network damarbagis_default -v "$(PWD)/loadtest:/scripts:ro" \
	  -e BASE_URL=http://api:8080 grafana/k6:0.54.0 run /scripts/load.js

chaos: up ## Kaos senaryosu: veritabanını durdurur, API'nin toparlandığını gösterir
	./scripts/chaos.sh

test: ## Birim testleri (veritabanı gerektirmez)
	go test ./... -count=1

test-integration: ## Entegrasyon testleri: TEST_DATABASE_URL gerekli
	go test ./... -count=1 -race

cover: ## Domain paketleri kapsam raporu
	go test ./internal/domain/... -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out | tail -1

lint: ## gofmt + go vet + golangci-lint
	test -z "$$(gofmt -l ./cmd ./internal)"
	go vet ./...
	golangci-lint run

openapi: ## OpenAPI dokümanını yeniden üretir
	python3 scripts/gen_openapi.py > internal/api/openapi.yaml
