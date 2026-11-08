//go:build dot

package voice

// Dot は天気の局所応答を持たない（weather_dot.go）ので、天気の発話は Jev の分類へ進む。
const weatherBypassesJev = false
