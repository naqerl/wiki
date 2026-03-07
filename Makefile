.PHONY: vet install run build clean

# Run formatting and linting tools
vet:
	go fmt ./...
	go vet ./...
	staticcheck ./...

# Install staticcheck
install:
	go install honnef.co/go/tools/cmd/staticcheck@latest

# Run the development server
run:
	go run . -port=${PORT:-8080}

# Build production binary to /tmp/wiki
build:
	go build -o /tmp/wiki .

clean:
	rm -f /tmp/wiki
