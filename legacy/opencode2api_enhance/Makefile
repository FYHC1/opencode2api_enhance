.PHONY: build test clippy release release-win installer clean

build:
	cargo build

test:
	cargo test

clippy:
	cargo clippy

release:
	cargo build --release

release-win: release
	@echo "构建完成: target\release\opencode2api-manager.exe"

installer: release-win
	iscc installer\manager-installer.iss

clean:
	cargo clean