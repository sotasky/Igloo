package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
)

func main() {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".local", "share", "igloo")
	dbPath := filepath.Join(dataDir, "igloo.db")
	d, err := db.OpenReadOnly(dbPath, dataDir)
	if err != nil {
		panic(err)
	}
	defer d.Close()

	mode, _ := d.GetSetting("dearrow_mode", "off")
	fmt.Println("dearrow_mode setting:", mode)

	for _, id := range []string{"jeA-KBv0b68", "zd6tBbCwkks", "-01ZCTt-CJw", "vrk40pZ8Kc8"} {
		v, err := d.GetVideo(id)
		if err != nil || v == nil {
			fmt.Println(id, "-- not found")
			continue
		}
		da := "<nil>"
		if v.DearrowTitle != nil {
			da = *v.DearrowTitle
		}
		fmt.Printf("\nvideo %s:\n  Title=%q\n  DearrowTitle=%s\n", id, v.Title, da)
		p := components.PrefsData{Settings: map[string]any{"dearrow_mode": mode}}
		fmt.Printf("  VideoTitle(v) via PrefsData=%q\n", p.VideoTitle(*v))
	}
}
