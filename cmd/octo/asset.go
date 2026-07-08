package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
)

// cmdAssetAdd uploads a local media file as a per-doc asset and prints the URL to
// reference in the doc's HTML. Author op → requires OCTO_TOKEN.
//
//	octo asset-add --slug <s> <file>
func cmdAssetAdd(args []string) error {
	fs := flag.NewFlagSet("asset-add", flag.ContinueOnError)
	slug := fs.String("slug", "", "slug of the doc (required)")
	quiet := fs.Bool("quiet", false, "print only the asset URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" {
		return fmt.Errorf("usage: octo asset-add --slug <slug> <file>")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: octo asset-add --slug <slug> <file>")
	}
	path := fs.Arg(0)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	cfg := loadConfig()
	cl, err := requireServer(cfg, true) // upload is author-only
	if err != nil {
		return err
	}
	res, err := cl.uploadAsset(context.Background(), *slug, filepath.Base(path), data)
	if err != nil {
		return err
	}
	if *quiet {
		fmt.Println(res.URL)
		return nil
	}
	fmt.Printf("Uploaded %s (%s, %d bytes)\n", filepath.Base(path), res.MIME, res.Size)
	fmt.Printf("Reference it in your HTML:\n\n  %s\n", res.URL)
	return nil
}

// cmdAssetList lists a doc's uploaded assets.
//
//	octo asset-list --slug <s>
func cmdAssetList(args []string) error {
	fs := flag.NewFlagSet("asset-list", flag.ContinueOnError)
	slug := fs.String("slug", "", "slug of the doc (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" {
		return fmt.Errorf("usage: octo asset-list --slug <slug>")
	}
	cfg := loadConfig()
	cl, err := requireServer(cfg, false) // listing needs only reader capability
	if err != nil {
		return err
	}
	list, err := cl.listAssets(context.Background(), *slug)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Printf("No assets for %s. Add one with `octo asset-add --slug %s <file>`.\n", *slug, *slug)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SHA256\tMIME\tSIZE\tNAME") //nolint:errcheck // tabwriter buffers; error surfaces at Flush
	for _, a := range list {
		sha := a.SHA256
		if len(sha) > 12 {
			sha = sha[:12]
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", sha, a.MIME, a.Size, a.OriginalName) //nolint:errcheck // see above
	}
	return tw.Flush()
}

// cmdAssetRm deletes one asset by its content hash. Author op.
//
//	octo asset-rm --slug <s> <sha256>
func cmdAssetRm(args []string) error {
	fs := flag.NewFlagSet("asset-rm", flag.ContinueOnError)
	slug := fs.String("slug", "", "slug of the doc (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" || fs.NArg() != 1 {
		return fmt.Errorf("usage: octo asset-rm --slug <slug> <sha256>")
	}
	cfg := loadConfig()
	cl, err := requireServer(cfg, true) // delete is author-only
	if err != nil {
		return err
	}
	if err := cl.deleteAsset(context.Background(), *slug, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Printf("Deleted asset %s from %s.\n", fs.Arg(0), *slug)
	return nil
}
