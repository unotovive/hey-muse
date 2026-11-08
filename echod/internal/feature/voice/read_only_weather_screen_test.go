//go:build !dot

package voice

// 画面付きの機種では「今日の天気」を端末内で答えるので、Jev へ分類を問い合わせない。
const weatherBypassesJev = true
