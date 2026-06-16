.PHONY: smoke
smoke:
	podman run --rm \
		--network none \
		-v $(PWD)/cmd/smoke:/work:Z \
		-w /work \
		--entrypoint sh \
		aicv/go-sandbox:latest \
		-c "go build -o /tmp/smoke-bin ./... && /tmp/smoke-bin"