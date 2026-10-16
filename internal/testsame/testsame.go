// Package testsame 提供门面壳测试的类型同一性编译期断言 helper。
//
// 显式声明类型再赋值会被 staticcheck（ST1023/QF1011）建议省略类型，但
// "值可否赋给目标类型"正是门面 alias 的被测语义——本 helper 以泛型实参
// 承载断言目标类型，编译期完成同一性检查且不触发上述建议。
package testsame

// Assert 断言 x 可赋给 T（编译期成立，运行时零开销）。
// 用法：testsame.Assert[*targetpkg.Type](facadeValue)。
func Assert[T any](x T) {}
