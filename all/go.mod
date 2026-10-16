module github.com/oekit/data/all

go 1.26.0

retract (
	// data 已在 v0.5.0 直接门面化（re-export 各子仓），本聚合模块整体废弃。
	v0.1.0
	v0.1.1
)
