.PHONY: build dev clean install generate-api

install:
	npm ci

generate-api:
	$(MAKE) -C ../.. docs

build: generate-api
	npm run build

dev: generate-api
	npm run dev

clean:
	rm -rf dist
