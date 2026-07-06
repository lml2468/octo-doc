package main

import (
	"fmt"
	"os"
	"text/tabwriter"
)

// cmdList prints the local docs: slug, latest version, open-comment count, title.
func cmdList(_ []string) error {
	cfg := loadConfig()
	st := newStore(cfg.Dir)
	slugs, err := st.listSlugs()
	if err != nil {
		return err
	}
	if len(slugs) == 0 {
		fmt.Println("No docs yet. Create one with `octo new`.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tVERSION\tOPEN\tTITLE") //nolint:errcheck // tabwriter buffers; error surfaces at Flush
	for _, slug := range slugs {
		meta, err := st.readMeta(slug)
		if err != nil {
			meta = &docMeta{Title: slug}
		}
		list, _ := st.readComments(slug)
		open := 0
		for _, c := range list {
			if c.Status == "open" {
				open++
			}
		}
		title := meta.Title
		if title == "" {
			title = slug
		}
		fmt.Fprintf(tw, "%s\tv%d\t%d\t%s\n", slug, meta.latestVersion(), open, title) //nolint:errcheck // see above
	}
	return tw.Flush()
}
