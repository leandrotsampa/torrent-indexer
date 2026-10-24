package handler

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

func newStarckTestDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}
	return doc
}

func Test_parseStarckFilmesListPost(t *testing.T) {
	doc := newStarckTestDoc(t, `<html><body>
<h1 class="o-titulo">Sujando as Mãos (2024)</h1>
<section class="o-lista" id="o-lista-dual_audio">
  <header class="o-grupo-cabecalho"><span class="o-grupo-nome">DUAL ÁUDIO</span><span class="o-grupo-qtd">2</span></header>
  <div class="o-arquivo">
    <span class="o-q q-fhd">1080P</span>
    <span class="o-arquivo-info"><b>2.04 GB</b> <small>WEB-DL</small></span>
    <span class="o-acoes-dl"><a class="o-baixar" href="magnet:?xt=urn:btih:059a1413b9e88f1fa7267fc9532db27361dd5a21&amp;dn=Sujando.as.Maos.2024.1080p.WEB-DL.DUAL">Baixar</a></span>
  </div>
  <div class="o-arquivo">
    <span class="o-arquivo-info"><b>900 MB</b></span>
    <span class="o-acoes-dl"><a class="o-baixar" href="https://example.com/not-a-magnet">Baixar</a></span>
  </div>
</section>
<section class="o-lista" id="o-lista-legendado">
  <header class="o-grupo-cabecalho"><span class="o-grupo-nome">LEGENDADO</span></header>
  <div class="o-arquivo">
    <span class="o-arquivo-info"><b>1.5 GB</b></span>
    <span class="o-acoes-dl"><a class="o-baixar" href="magnet:?xt=urn:btih:159a1413b9e88f1fa7267fc9532db27361dd5a21&amp;dn=Sujando.as.Maos.2024.1080p.LEG">Baixar</a></span>
  </div>
</section>
</body></html>`)

	title, year, magnets := parseStarckFilmesListPost(doc)
	if title != "Sujando as Mãos" || year != "2024" {
		t.Fatalf("title/year = %q/%q, want Sujando as Mãos/2024", title, year)
	}
	if len(magnets) != 2 {
		t.Fatalf("len(magnets) = %d, want 2: %+v", len(magnets), magnets)
	}
	if !strings.HasPrefix(magnets[0].link, "magnet:?xt=urn:btih:059a") || magnets[0].size != "2.04 GB" {
		t.Errorf("magnets[0] = %+v", magnets[0])
	}
	if len(magnets[0].audio) != 1 || magnets[0].audio[0] != schema.AudioPortuguese {
		t.Errorf("magnets[0].audio = %v, want [Portuguese]", magnets[0].audio)
	}
	if magnets[1].size != "1.5 GB" || len(magnets[1].audio) != 0 {
		t.Errorf("magnets[1] = %+v, want size 1.5 GB and no audio", magnets[1])
	}
}

func Test_parseStarckFilmesListPost_TitleWithoutYear(t *testing.T) {
	doc := newStarckTestDoc(t, `<html><body><h1> Confinada Torrent </h1><section class="o-lista"></section></body></html>`)

	title, year, magnets := parseStarckFilmesListPost(doc)
	if title != "Confinada Torrent" || year != "" || len(magnets) != 0 {
		t.Fatalf("got %q/%q/%v", title, year, magnets)
	}
}

func Test_parseStarckFilmesLegacyPost(t *testing.T) {
	doc := newStarckTestDoc(t, `<html><body><div class="post">
<div class="capa"><div class="post-description">
  <h2>Extermínio 2</h2>
  <p><span>Lançamento:</span><span>2007</span></p>
  <p><span>Tamanho:</span><span>2.45 GB</span></p>
  <p><span>Idioma:</span><span>Português | Inglês</span></p>
</div></div>
<div class="post-buttons">
  <a href="magnet:?xt=urn:btih:259a1413b9e88f1fa7267fc9532db27361dd5a21&amp;dn=Exterminio.2.2007.DUAL">Baixar</a>
</div>
</div></body></html>`)

	title, year, magnets := parseStarckFilmesLegacyPost(doc)
	if title != "Extermínio 2" || year != "2007" {
		t.Fatalf("title/year = %q/%q, want Extermínio 2/2007", title, year)
	}
	if len(magnets) != 1 {
		t.Fatalf("len(magnets) = %d, want 1", len(magnets))
	}
	if magnets[0].size != "2.45 GB" {
		t.Errorf("size = %q, want 2.45 GB", magnets[0].size)
	}
	if len(magnets[0].audio) != 2 {
		t.Errorf("audio = %v, want Portuguese and English", magnets[0].audio)
	}
}

func Test_starckGroupAudio(t *testing.T) {
	for name, wantPT := range map[string]bool{
		"DUAL ÁUDIO": true,
		"DUBLADO":    true,
		"Nacional":   true,
		"LEGENDADO":  false,
		"":           false,
	} {
		got := starckGroupAudio(name)
		if (len(got) == 1 && got[0] == schema.AudioPortuguese) != wantPT {
			t.Errorf("starckGroupAudio(%q) = %v, want Portuguese=%v", name, got, wantPT)
		}
	}
}
