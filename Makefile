.PHONY: test test-race test-coverage lint fmt vet ci tidy check-errkey

## test: 运行所有单元测试
test:
	go test ./...

## test-race: 运行竞态检测测试
test-race:
	go test -race ./...

## test-coverage: 生成测试覆盖率报告
test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

## lint: 运行 golangci-lint（需要先安装）
lint:
	golangci-lint run ./...

## fmt: 格式化所有 Go 文件
fmt:
	gofmt -s -w .
	goimports -w .

## vet: 运行 go vet
vet:
	go vet ./...

## ci: 模拟 CI 流程（fmt 检查 + 硬规则门禁 + vet + test-race）
ci:
	@test -z "$$(gofmt -l . | grep -v vendor | tee /dev/stderr)" || (echo "请运行 make fmt" && exit 1)
	@$(MAKE) check-errkey
	go vet ./...
	go test -race -count=1 ./...

## check-errkey: AGENTS.md 硬规则门禁——错误字段名禁止手写 "err"/"error"
## 字面量（两矩阵落同一 Loki/ES，字段名会分裂），统一用 logx.ErrKey
## （#200；测试文件断言日志字段名属合法用法，豁免 _test.go）。
check-errkey:
	@test -z "$$(grep -rn '"err"\|"error"' --include='*.go' . | grep -v _test.go | grep -v vendor | tee /dev/stderr)" || (echo "发现手写 err 字面量，请改用 logx.ErrKey（AGENTS.md 硬规则）" && exit 1)

## tidy: 整理 go.mod
tidy:
	go mod tidy

## help: 显示帮助
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
