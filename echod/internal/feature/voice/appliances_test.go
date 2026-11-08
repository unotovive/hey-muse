package voice

import "testing"

func TestApplianceCommands(t *testing.T) {
	for phrase, want := range map[string]struct {
		target, action string
		value          int
	}{
		"電気消して": {"living", "off", 0}, "電気を消してください。": {"living", "off", 0},
		"モニター消して": {"monitor", "toggle", 0}, "モニター付けて": {"monitor", "toggle", 0},
		"モニターをつけて。": {"monitor", "toggle", 0}, "モニターを点けて": {"monitor", "toggle", 0},

		"扇風機を回して": {"fan", "on", 0}, "エアコンを止めて": {"ac", "off", 0}, "リビングの照明を消灯して": {"living", "off", 0},
		"廊下のライトをオフにして。":    {"hall", "off", 0},
		"リビングの電気つけて":       {"living", "on", 0},
		"リビングの照明を消して。":     {"living", "off", 0},
		"エアコンの電源を入れてください":  {"ac", "on", 0},
		"サーキュレーターをつけて":     {"fan", "on", 0},
		"扇風機を消して":          {"fan", "off", 0},
		"プラグミニをオンにして":      {"plug", "on", 0},
		"掃除機の電源を切って":       {"vacuum", "off", 0},
		"カーテンを開けて":         {"curtain", "open", 0},
		"カーテンを閉めてください":     {"curtain", "close", 0},
		"モニターの電源を切り替えて":    {"monitor", "toggle", 0},
		"プロジェクターの電源を切り替えて": {"projector", "toggle", 0},
		"エアコンを冷房にして":       {"ac", "cool", 0}, "冷房つけて": {"ac", "cool", 0}, "暖房をつけて": {"ac", "heat", 0},
		"エアコンを暖房にして": {"ac", "heat", 0}, "エアコンを26度にして": {"ac", "temp", 26}, "エアコン２４度にして": {"ac", "temp", 24},
		"エアコンを16度にして": {"ac", "temp", 16}, "エアコン３０度にして": {"ac", "temp", 30},
		"冷房を消して": {"ac", "off", 0}, "暖房消して": {"ac", "off", 0},
	} {
		got, ok := parseAppliance(phrase)
		if !ok || got.target != want.target || got.action != want.action || got.value != want.value {
			t.Errorf("%q => %+v, %t", phrase, got, ok)
		}
	}
	h := hallTurns{}
	for _, phrase := range []string{"ボーカルライト消した", "電気を消して？", "電気消さないで", "「電気消して」と言って", "カーテンを開けないで", "カーテンを開けて？", "モニターをつけていい？", "モニター消さないで", "モニター消してから付けて", "プロジェクターを消して", "ミラーボールをつけて", "エアコンと扇風機をつけて", "「掃除機をつけて」と言って", "リビングの電気を消してからつけて", "それを開けて", "エアコンを26度にしないで", "冷房にしてって言われた", "「暖房をつけて」と言って", "エアコンを何度にする？", "エアコンを100度にして", "エアコンを15度にして", "エアコンを31度にして", "エアコンを5度にして"} {
		if _, ok := parseAppliance(phrase); ok || !h.hold("chat", phrase) {
			t.Errorf("unsafe routing for %q", phrase)
		}
	}
}

func TestApplianceTemperatureHold(t *testing.T) {
	h := hallTurns{}
	for phrase, want := range map[string]bool{
		"お風呂を40度にして": false, "オーブンを200度に設定して": false, "お湯を42度にして": false,
		"エアコンを100度にして": true, "26度にして": true, "２６度にして": true, "エアコンを26度に設定して": true,
	} {
		if got := h.hold("chat", phrase); got != want {
			t.Errorf("hold(%q) = %t, want %t", phrase, got, want)
		}
	}
}
