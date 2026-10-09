.PHONY: build test lint build-android

build:
	go build -o bin/ ./cmd/...

test:
	go test ./...

lint:
	go vet ./...

# Binário de conveniência; a validade no Termux (DNS/SSL, R-13) só vale após build no aparelho.
build-android:
	GOOS=linux GOARCH=arm64 go build -o bin/android-arm64/ ./cmd/...
