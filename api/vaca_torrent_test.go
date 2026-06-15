package handler

import "testing"

func Test_decodeVacaDataLink(t *testing.T) {
	want := "magnet:?xt=urn:btih:CF76LOCCN4KECVLBWM4D5TKIIRJKYZMM&tr=udp://tracker.example/announce"
	got, err := decodeVacaDataLink("bWFnbmV0Oj94dD11cm46YnRpaDpDRjc2TE9DQ040S0VDVkxCV000RDVUS0lJUkpLWVpNTSZ0cj11ZHA6Ly90cmFja2VyLmV4YW1wbGUvYW5ub3VuY2U=")
	if err != nil {
		t.Fatalf("decodeVacaDataLink() failed: %v", err)
	}
	if got != want {
		t.Fatalf("decodeVacaDataLink() = %q, want %q", got, want)
	}
}

func Test_getVacaJSString(t *testing.T) {
	body := []byte(`const next = "https:\/\/t.co\/abc123"; var URL_ETAPA2 = "https:\/\/vacaflix.xyz\/enc\/receber.php?enc=abc&pub=PUB";`)

	if got := getVacaJSString(body, vacaSystemTechNextRE); got != "https://t.co/abc123" {
		t.Fatalf("next URL = %q", got)
	}
	if got := getVacaJSString(body, vacaSecondStageURLRE); got != "https://vacaflix.xyz/enc/receber.php?enc=abc&pub=PUB" {
		t.Fatalf("second stage URL = %q", got)
	}
}

func Test_getVacaDataLinkMagnetsFromHTML(t *testing.T) {
	body := []byte(`<html><body data-link="bWFnbmV0Oj94dD11cm46YnRpaDpDRjc2TE9DQ040S0VDVkxCV000RDVUS0lJUkpLWVpNTQ=="></body></html>`)

	got := getVacaDataLinkMagnetsFromHTML(body)
	if len(got) != 1 {
		t.Fatalf("got %d magnets, want 1", len(got))
	}
	if got[0] != "magnet:?xt=urn:btih:CF76LOCCN4KECVLBWM4D5TKIIRJKYZMM" {
		t.Fatalf("magnet = %q", got[0])
	}
}
