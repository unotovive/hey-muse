package voice

import (
	"regexp"
	"strconv"
	"strings"
)

type applianceCommand struct {
	target, action, label string
	value                 int
}
type voiceAppliance struct {
	id, name, kind string
	names          []string
}

var defaultLightOff = regexp.MustCompile(`^(?:電気|でんき)(?:を)?[、 ]?(?:消して|けして)$`)

var (
	acMode    = regexp.MustCompile(`^(?:エアコン(?:を|の)?)?(冷房|暖房)(?:に(?:して|切り替えて)|を?(?:つけて|入れて))$`)
	acTemp    = regexp.MustCompile(`^エアコン(?:を)?([0-9０-９]{2})度に(?:して|設定して)$`)
	acModeOff = regexp.MustCompile(`^(?:冷房|暖房)(?:を)?(?:消して|切って|止めて)$`)
)

var voiceAppliances = []voiceAppliance{
	{"living", "リビングの照明", "power", []string{"リビングの電気", "リビングの照明", "リビングのライト", "リビングの明かり", "リビングの灯り"}},
	{"ac", "エアコン", "power", []string{"エアコン", "クーラー"}},
	{"fan", "サーキュレーター", "power", []string{"サーキュレーター", "サーキュレータ", "扇風機"}},
	{"plug", "プラグミニ", "power", []string{"プラグミニ", "スマートプラグ"}},
	{"vacuum", "掃除機", "power", []string{"掃除機"}},
	{"curtain", "カーテン", "curtain", []string{"カーテン"}},
	{"monitor", "モニター", "monitorToggle", []string{"モニター", "モニタ"}},
	{"projector", "プロジェクター", "toggle", []string{"プロジェクター", "プロジェクタ"}},
}

func applianceAction(kind, verb string) (action, description string) {
	switch kind {
	case "power":
		switch verb {
		case "つけて", "付けて", "点けて", "入れて", "オンにして", "onにして":
			return "on", "オン"
		case "消して", "けして", "切って", "オフにして", "offにして":
			return "off", "オフ"
		}
	case "curtain":
		switch verb {
		case "開けて", "あけて", "開いて":
			return "open", "開く操作"
		case "閉めて", "しめて", "閉じて":
			return "close", "閉じる操作"
		}
	case "monitorToggle":
		switch verb {
		case "つけて", "付けて", "点けて", "消して", "けして":
			return "toggle", "電源切替"
		}
		fallthrough
	case "toggle":
		if verb == "切り替えて" || verb == "切替えて" || verb == "切りかえて" {
			return "toggle", "電源切替"
		}
	}
	return "", ""
}

func parseAppliance(text string) (applianceCommand, bool) {
	if direction := hallDirection(text); direction != "" {
		label := "廊下の点灯"
		if direction == "off" {
			label = "廊下の消灯"
		}
		return applianceCommand{target: "hall", action: direction, label: label}, true
	}
	phrase := strings.ToLower(strings.TrimSpace(text))
	phrase = strings.TrimRight(phrase, "。.!！")
	phrase = strings.TrimSuffix(strings.TrimSuffix(phrase, "ください"), "下さい")
	if defaultLightOff.MatchString(phrase) {
		return applianceCommand{target: "living", action: "off", label: "リビングの照明のオフ"}, true
	}
	for _, device := range voiceAppliances {
		for _, name := range device.names {
			tail, ok := strings.CutPrefix(phrase, name)
			if !ok {
				continue
			}
			if device.kind != "curtain" {
				tail = strings.TrimPrefix(tail, "の電源")
			}
			verb := strings.TrimLeft(strings.TrimPrefix(tail, "を"), "、 ")
			if device.id == "fan" && verb == "回して" {
				verb = "つけて"
			}
			if (device.id == "fan" || device.id == "ac") && verb == "止めて" {
				verb = "消して"
			}
			if device.id == "living" && verb == "点灯して" {
				verb = "つけて"
			}
			if device.id == "living" && verb == "消灯して" {
				verb = "消して"
			}
			action, description := applianceAction(device.kind, verb)
			if action != "" {
				return applianceCommand{target: device.id, action: action, label: device.name + "の" + description}, true
			}
		}
	}
	if parts := acMode.FindStringSubmatch(phrase); parts != nil {
		if parts[1] == "冷房" {
			return applianceCommand{target: "ac", action: "cool", label: "エアコンの冷房"}, true
		}
		return applianceCommand{target: "ac", action: "heat", label: "エアコンの暖房"}, true
	}
	if parts := acTemp.FindStringSubmatch(phrase); parts != nil {
		digits := strings.Map(func(r rune) rune {
			if r >= '０' && r <= '９' {
				return r - '０' + '0'
			}
			return r
		}, parts[1])
		if degrees, err := strconv.Atoi(digits); err == nil && degrees >= 16 && degrees <= 30 {
			return applianceCommand{target: "ac", action: "temp", label: "エアコンの" + digits + "度設定", value: degrees}, true
		}
	}
	if acModeOff.MatchString(phrase) {
		return applianceCommand{target: "ac", action: "off", label: "エアコンのオフ"}, true
	}
	return applianceCommand{}, false
}
