package voice

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/mothership"
	"github.com/HuskerMinion/techo5/echod/internal/lib/standalone"
	"github.com/google/uuid"
)

var hallCommand = regexp.MustCompile(`^廊下の(?:電気|でんき|照明|ライト|明かり|灯り)(?:を)?[、 ]?(つけて|点けて|付けて|消して|けして|点灯して|消灯して|オンにして|オフにして|(?i:on|off)にして)(?:ください|下さい)?[。.!！]?$`)

func hallDirection(text string) string {
	parts := hallCommand.FindStringSubmatch(strings.TrimSpace(text))
	if parts == nil {
		return ""
	}
	switch strings.ToLower(parts[1]) {
	case "消して", "けして", "消灯して", "オフにして", "offにして":
		return "off"
	default:
		return "on"
	}
}

// Keep unrecognized appliance requests away from the general Muse chat, which
// has its own home tools and may otherwise repair a misheard target and act.
var powerWords = regexp.MustCompile(`つけ|付け|点け|消[すしさせえ]|けして|けさな|灯り|点灯|消灯|明るく|暗く|オン|オフ|入れ|切[っるら]|ついて|付いて`)
var otherDevice = regexp.MustCompile(`リビング|サーキュレータ|扇風機|クーラー|掃除機|カーテン|プラグ|プロジェクタ|モニタ|ミラーボール`)
var otherAction = regexp.MustCompile(`開け|あけて|開い|閉め|しめて|閉じ|切り替|切替|動か|止め|回し|スタート|ストップ|電源`)
var applianceWords = regexp.MustCompile(`廊下|ろうか|電気|でんき|照明|ライト|電源|明かり|エアコン|モニター`)
var specificDevice = regexp.MustCompile(`電気|でんき|照明|ライト|灯り|明かり|電源|エアコン|モニター`)
var requestEnding = regexp.MustCompile(`(?:して|入れて|落として|やめて|しないで|ないで|ますか|なの|ですか|[？?])(?:ください|下さい)?[。！？!?]?$`)
var barePower = regexp.MustCompile(`^(?:それを?|そこを?|あれを?)?(?:つけ|付け|点け|けして|消[すしさせえ]|オンに|オフに|点灯して|消灯して|開け|あけて|閉め|しめて|切り替え|切替え)`)
var repeatPower = regexp.MustCompile(`^(もう一度|もう一回|もういっかい|やり直し|またやって|同じこと|それをやって|お願い)[。！!？?]?$`)

func (h *hallTurns) hold(session, text string) bool {
	if otherDevice.MatchString(text) && (powerWords.MatchString(text) || otherAction.MatchString(text) || requestEnding.MatchString(text)) || barePower.MatchString(text) || applianceWords.MatchString(text) && powerWords.MatchString(text) ||
		specificDevice.MatchString(text) && requestEnding.MatchString(text) {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.session == session && h.receipt != "" && repeatPower.MatchString(text)
}

// Only the latest receipt within the same wake chat is relevant on a follow-up.
type hallTurns struct {
	mu               sync.Mutex
	session, receipt string
}

func hallHoldReply(text string) string {
	if repeatPower.MatchString(text) {
		return "もう一度操作する場合は、機器名と操作を言ってください。"
	}
	if specificDevice.MatchString(text) || otherDevice.MatchString(text) || applianceWords.MatchString(text) {
		if strings.Contains(text, "ミラーボール") {
			return "ミラーボールは音声操作の対象外です。"
		}
		if strings.Contains(text, "ないで") || strings.Contains(text, "しない") || strings.Contains(text, "ないよう") {
			return "操作を見送りました。"
		}
		if strings.ContainsAny(text, "？?") || strings.HasSuffix(strings.TrimRight(text, "。"), "ますか") {
			return "質問として聞き取りました。操作する場合は、機器名と操作を言ってください。"
		}
		for _, device := range voiceAppliances {
			for _, name := range device.names {
				if !strings.Contains(text, name) {
					continue
				}
				switch device.kind {
				case "monitorToggle":
					return "「モニター消して」か「モニターつけて」と言うと、電源を一回切り替えます。"
				case "toggle":
					return device.name + "は電源切替に対応しています。「" + device.name + "の電源を切り替えて」と言ってください。"
				case "curtain":
					return "「カーテンを開けて」か「カーテンを閉めて」と言ってください。"
				default:
					return "操作を聞き取れませんでした。「" + device.name + "をつけて」か「" + device.name + "を消して」と言ってください。"
				}
			}
		}
		if strings.Contains(text, "廊下") {
			return "「廊下の電気をつけて」か「廊下の電気を消して」と言ってください。"
		}
		if strings.Contains(text, "ライト") || strings.Contains(text, "照明") || strings.Contains(text, "電気") {
			return "どこの照明ですか。「廊下の電気を消して」のように、部屋と操作を言ってください。"
		}
	}
	return "機器名と操作を聞き取れませんでした。もう一度言ってください。"
}

func (h *hallTurns) withReceipt(session, text string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if session == h.session && h.receipt != "" {
		message := "直前の機器操作記録（再実行不要）：" + h.receipt + "\n現在の発話：" + text
		h.receipt = ""
		return message
	}
	return text
}

func (h *hallTurns) executeCommand(ctx context.Context, session string, command applianceCommand) string {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	raw, err := os.ReadFile("/data/misc/techo5/idle-home.json")
	var cfg struct{ URL, Key string }
	ok := false
	start := time.Now()
	if standalone.Device.Enabled() {
		ok = standalone.Device.Appliance(ctx, command.target, command.action, 0) == nil
	} else if err == nil && json.Unmarshal(raw, &cfg) == nil && strings.HasSuffix(cfg.URL, "/echo-home/v1") {
		var result struct{ OK bool }
		url := strings.TrimSuffix(cfg.URL, "/echo-home/v1") + "/echo-hall/v1"
		payload := map[string]any{"op": "hall", "id": uuid.NewString(), "direction": command.action, "time": time.Now().Unix()}
		if command.target != "hall" {
			payload["op"] = "appliance"
			payload["target"] = command.target
		}
		err = mothership.HomeRequest(ctx, url, cfg.Key, payload, &result)
		ok = err == nil && result.OK
	}
	if command.target == "hall" {
		slog.Info("hall command", "direction", command.action, "accepted", ok, "roundtrip_ms", time.Since(start).Milliseconds())
	} else {
		slog.Info("voice appliance command", "target", command.target, "action", command.action, "accepted", ok, "roundtrip_ms", time.Since(start).Milliseconds())
	}
	receipt := command.label + "を受け付けました。"
	if !ok {
		receipt = command.label + "を受け付けたか確認できませんでした。"
	}
	h.mu.Lock()
	h.session, h.receipt = session, receipt
	h.mu.Unlock()
	return receipt
}
