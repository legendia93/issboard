package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/legendia93/issboard/internal/ops"
)

// Journal dibaca grup adm. Token yang tercatat di sana adalah token yang bocor.
func TestAuditTidakMencatatToken(t *testing.T) {
	req := ops.Request{Action: ops.NotifySet, Props: map[string]string{
		"ISSBOARD_TELEGRAM_TOKEN":   "123456789:RAHASIARAHASIARAHASIARAHASIARAHASIA",
		"ISSBOARD_TELEGRAM_CHAT_ID": "",
	}}
	got := fmt.Sprint(auditProps(req))
	if strings.Contains(got, "RAHASIA") {
		t.Fatalf("token masuk log audit: %s", got)
	}
	if !strings.Contains(got, "ISSBOARD_TELEGRAM_TOKEN") || !strings.Contains(got, "CHAT_ID(hapus)") {
		t.Errorf("nama kunci harus tetap tercatat: %s", got)
	}
}
