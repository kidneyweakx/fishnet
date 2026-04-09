.PHONY: build install clean test

build:
	go build -o fishnet .

install:
	go install .

clean:
	rm -f fishnet
	go clean

test:
	go test ./...

# Quick demo (requires OPENAI_API_KEY or ANTHROPIC_API_KEY)
demo:
	mkdir -p demo-project
	./fishnet init demo --dir ./demo-project
	@echo "Put .txt or .md files in demo-project/, then run:"
	@echo "  ./fishnet analyze"
	@echo "  ./fishnet graph web"
	@echo "  ./fishnet sim run --scenario 'Launch a new product'"
