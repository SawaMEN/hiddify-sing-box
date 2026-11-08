package rule

import (
    "context"
    "testing"

    "github.com/sagernet/sing-box/option"
    "github.com/sagernet/sing/common/json"
)

func TestXUIPanelSniffOverrideAction(t *testing.T) {
    for _, enabled := range []bool{false, true} {
        raw := `{"action":"sniff","sniffer":["http","tls"],"override_destination":false}`
        if enabled { raw = `{"action":"sniff","sniffer":["http","tls"],"override_destination":true}` }
        var options option.RuleAction
        if err := json.Unmarshal([]byte(raw), &options); err != nil { t.Fatal(err) }
        action, err := NewRuleAction(context.Background(), nil, options)
        if err != nil { t.Fatal(err) }
        sniff, ok := action.(*RuleActionSniff)
        if !ok || sniff.OverrideDestination != enabled || len(sniff.StreamSniffers) != 2 {
            t.Fatalf("native decoder/action lost sniff policy: %#v", action)
        }
    }
}
