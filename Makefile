.PHONY: build test lint build-android e2e

build:
	go build -o bin/ ./cmd/...

test:
	go test ./...

lint:
	go vet ./...

# Binário de conveniência; a validade no Termux (DNS/SSL, R-13) só vale após build no aparelho.
build-android:
	GOOS=linux GOARCH=arm64 go build -o bin/android-arm64/ ./cmd/...

# e2e no aparelho (Termux): roda as suítes de test/e2e com runit, Caddy, sshd e git reais num sandbox isolado.
e2e:
	SRC=$(CURDIR) bash test/e2e/run-all.sh
