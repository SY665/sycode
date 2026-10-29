# 薄封装：真正的逻辑在 scripts/check.sh，Windows 无 make 时照样能跑。
# 用法：make check / make fmt / make test
.PHONY: check fmt vet build test race tidy clean

SHELL := bash

check: ## 提交前必跑：格式 + vet + 构建 + 测试 + 竞态
	@bash scripts/check.sh

fmt: ## 就地格式化
	@gofmt -l -w .

vet:
	@go vet ./...

build:
	@go build ./...

test:
	@go test ./...

race:
	@go test -race ./...

tidy:
	@go mod tidy

clean:
	@rm -f sycode sycode.exe
