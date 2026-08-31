.PHONY: build install test relay run clean

build:
	./scripts/build.sh

install: build
	cp dist/reminal /usr/local/bin/reminal

test:
	go test ./...

relay: build
	./dist/reminal relay

run: build
	REMINAL_LOCAL=1 ./dist/reminal

clean:
	rm -rf dist/
