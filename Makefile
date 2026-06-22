.PHONY: smoke
smoke:
	podman run --rm \
		--network none \
		-v $(PWD)/cmd/smoke:/work:Z \
		-w /work \
		--entrypoint sh \
		aicv/go-sandbox:latest \
		-c "go build -o /tmp/smoke-bin ./... && /tmp/smoke-bin"

smoke_rust:
	podman run --rm --network none rust-sandbox sh -c '
		mkdir -p /tmp/exec/test/src
		cd /tmp/exec/test
		cat > Cargo.toml << INNER
		[package]
		name = "test"
		version = "0.1.0"
		edition = "2021"
		[dependencies]
		serde = "1"
		INNER
		cat > src/main.rs << INNER
		use serde::Serialize;
		fn main() {
			let x: i32 = "hello";
		}
		INNER
		echo "=== HUMAN ==="
		cargo build --offline 2>&1
		echo ""
		echo "=== JSON ==="
		cargo build --offline --message-format=json 2>&1 | grep "error"
		'