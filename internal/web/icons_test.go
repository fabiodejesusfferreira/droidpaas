package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	webfs "droidpaas/web"
)

// Todo {{template "ic" "nome"}} precisa ter o <symbol id="i-nome"> no sprite (senão o ícone some em silêncio).
func TestIconsExistInSprite(t *testing.T) {
	sprite, err := webfs.FS.ReadFile("templates/partials/icons.html")
	if err != nil {
		t.Fatal(err)
	}
	use := regexp.MustCompile(`\{\{template "ic" "([a-z-]+)"\}\}`)
	n := 0
	fs.WalkDir(webfs.FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := webfs.FS.ReadFile(p)
		for _, m := range use.FindAllStringSubmatch(string(b), -1) {
			n++
			if !strings.Contains(string(sprite), `id="i-`+m[1]+`"`) {
				t.Errorf("%s usa o ícone %q, que não está em partials/icons.html", p, m[1])
			}
		}
		return nil
	})
	if n == 0 {
		t.Fatal("nenhum ícone encontrado nos templates")
	}
}
