// Package templates 做简单的 {placeholder} 文案替换。
// 不用 text/template，因为文案里花括号语义简单、用户编辑直观。
package templates

import "strings"

// Render 把 text 中所有 {key} 替换为 vars[key]。未提供的占位符原样保留。
func Render(text string, vars map[string]string) string {
	if len(vars) == 0 {
		return text
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
