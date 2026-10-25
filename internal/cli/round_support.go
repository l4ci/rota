package cli

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

// The support split (#1598): main labels the issues the support machine may
// take. offer, enable and disable are fenced on a support machine
// (roundcfg support fence), so they run on main only; return is the support machine's
// and refuses main inside the verb.
func roundSupport() *Command {
	return &Command{Name: "support", Summary: "split work with the support machine (round.support)", Subs: []*Command{
		{Name: "offer", Summary: "label issues for the support machine; --withdraw takes them back", Verb: roundSupportOffer},
		{Name: "return", Summary: "hand an offered issue back to main, with a reason (support machine)", Verb: roundSupportReturn},
		{Name: "enable", Summary: "name the support machine in the tracked config (round.support)", Verb: roundSupportEnable},
		{Name: "disable", Summary: "remove round.support from the tracked config", Verb: roundSupportDisable},
	}}
}

// supportConfigFile is where enable and disable write: the tracked config, so
// the name reaches the support machine with the next pull (#1774).
var supportConfigFile = rotatree.DirName + "/" + rotatree.ConfigFile

func roundSupportEnable(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round support enable takes the support machine's name")
		}
		name := args[0]
		if !roundcfg.ValidName(name) {
			return Result{}, Usage("round support enable: %q is not a lowercase name usable in a branch (letters, digits, -)", name)
		}
		if err := config.Validate("round.support", strconv.Quote(name)); err != nil {
			return Result{}, Usage("%s", err)
		}
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return supportFileModeStop()
		}
		// Quoted, so a name such as "true" stays a string.
		res, err := config.Set(root, "round.support", strconv.Quote(name))
		if err != nil {
			return Result{}, err
		}
		text := fmt.Sprintf("round.support = %s (%s); commit it, then run rota setup with round.role support on the %s machine", name, supportConfigFile, name)
		if !res.Changed {
			text = fmt.Sprintf("noop: round.support is already %s", name)
		}
		return Result{Data: supportConfigData(name, res.Changed), Text: text}, nil
	}
}

func roundSupportDisable(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 0 {
			return Result{}, Usage("round support disable takes no arguments")
		}
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return supportFileModeStop()
		}
		name := roundcfg.SupportName(fsio.LoadJSON(rotatree.Config(root), jsonx.NewObject()))
		removed, err := config.Unset(root, "round.support", config.LayerProject)
		if err != nil {
			return Result{}, err
		}
		text := fmt.Sprintf("removed round.support from %s; commit it", supportConfigFile)
		if !removed {
			text = "noop: round.support is not set in " + supportConfigFile
		}
		return Result{Data: supportConfigData(name, removed), Text: text}, nil
	}
}

func supportConfigData(name string, changed bool) *jsonx.Object {
	return jsonObj("name", name, "file", supportConfigFile, "changed", changed)
}

func roundSupportOffer(fs *flag.FlagSet) RunFunc {
	withdraw := fs.Bool("withdraw", false, "remove the support label instead of adding it")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) == 0 {
			return Result{}, Usage("round support offer takes one or more item IDs")
		}
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return supportFileModeStop()
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		ids := make([]string, 0, len(args))
		for _, a := range args {
			id, _, err := resolveItem(be, a)
			if err != nil {
				return backlogFail(err)
			}
			ids = append(ids, id)
		}
		changed, err := env.OfferSupport(c.Context(), root, be, ids, *withdraw)
		if err != nil {
			return moveFailure(err, len(changed) > 0)
		}
		verb := "offered to the support machine"
		if *withdraw {
			verb = "withdrawn from the support machine"
		}
		refs := make([]string, 0, len(changed))
		for _, id := range changed {
			refs = append(refs, tracker.RefOf(id))
		}
		text := "nothing to change: every issue already is " + verb
		if len(refs) > 0 {
			text = fmt.Sprintf("%s %s", strings.Join(refs, ", "), verb)
		}
		return Result{Data: jsonObj("ids", append([]string{}, changed...), "withdraw", *withdraw, "changed", len(changed) > 0), Text: text}, nil
	}
}

// roundSupportReturn is the support machine's hand-back (#1827): the reason
// goes on the issue and the support label comes off.
func roundSupportReturn(fs *flag.FlagSet) RunFunc {
	reason := fs.String("reason-file", "", "why the issue goes back; posted on it (- for stdin)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round support return takes one item ID")
		}
		if *reason == "" {
			return Result{}, Usage("--reason-file is required: say why the issue goes back")
		}
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return supportFileModeStop()
		}
		text, err := roundNote(c, "reason-file", *reason)
		if err != nil {
			return Result{}, err
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		id, _, err := resolveItem(be, args[0])
		if err != nil {
			return backlogFail(err)
		}
		res, err := env.ReturnSupport(c.Context(), root, be, id, text)
		if err != nil {
			return moveFailure(err, res.Changed)
		}
		out := fmt.Sprintf("noop: %s is not labelled for the support machine", tracker.RefOf(res.ID))
		if res.Changed {
			out = "returned " + tracker.RefOf(res.ID)
		}
		return Result{Data: jsonObj("id", res.ID, "comment", res.Comment, "changed", res.Changed), Text: out}, nil
	}
}
