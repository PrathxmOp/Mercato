.PHONY: all build templ run clean

all: templ build

templ:
	templ generate

build:
	go build -o bin/mercato cmd/mercato/main.go

run: templ build
	./bin/mercato

clean:
	rm -rf bin/
	rm -f view/**/*_templ.go
