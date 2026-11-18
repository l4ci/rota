package worker

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/postgate"
)

// Every difference needs a reason; every shared field must retain its value.
// Adding a field on either side therefore requires updating the adapter.
func TestLandingOptionMapping(t *testing.T) {
	t.Run("gate", func(t *testing.T) {
		opts := populatedOptions[postgate.GateOpts](t)
		checkOptionMapping(t, opts, gateOptions(opts), map[string]string{
			"PRBase":           "train member PR target differs from its landing base",
			"LandVerified":     "train already verified this landing",
			"LandVerifiedTip":  "train's verified head",
			"LandVerifiedBase": "train's verified base",
			"HoldsLandLock":    "train owns the landing lock",
			"NoVerdict":        "train already judged verdicts",
			"ReviewsOnly":      "autopilot review probe",
			"Observe":          "read-only autopilot probe",
			"BehaviourDir":     "train's scratch tree",
			"in":               "train's loaded gate input",
		})
	})
	t.Run("train", func(t *testing.T) {
		opts := populatedOptions[postgate.TrainOpts](t)
		checkOptionMapping(t, opts, trainOptions(opts), map[string]string{
			"ontoBase": "resolved internally from the integration stack",
		})
	})
}

type optionApproval struct{}

func (optionApproval) Check(string, func() ([]string, error), func() (landing.ApprovalThread, error)) (*exitcode.Refusal, error) {
	return nil, nil
}

func populatedOptions[T any](t *testing.T) T {
	t.Helper()
	var opts T
	v := reflect.ValueOf(&opts).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Bool:
			f.SetBool(true)
		case reflect.String:
			f.SetString(v.Type().Field(i).Name)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{v.Type().Field(i).Name}))
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
		case reflect.Interface:
			f.Set(reflect.ValueOf(optionApproval{}))
		case reflect.Func:
			f.Set(reflect.MakeFunc(f.Type(), func([]reflect.Value) []reflect.Value {
				t.Fatal("option conversion invoked a callback")
				return nil
			}))
		default:
			t.Fatalf("populate new option %s (%s)", v.Type().Field(i).Name, f.Type())
		}
	}
	return opts
}

func checkOptionMapping(t *testing.T, source, target any, executionOnly map[string]string) {
	t.Helper()
	from, to := reflect.ValueOf(source), reflect.ValueOf(target)
	for i := 0; i < from.NumField(); i++ {
		name := from.Type().Field(i).Name
		got := to.FieldByName(name)
		if !got.IsValid() {
			t.Errorf("postgate option %s has no worker field", name)
			continue
		}
		want := from.Field(i)
		if !want.Type().AssignableTo(got.Type()) {
			t.Errorf("option %s types differ: %s / %s", name, want.Type(), got.Type())
			continue
		}
		equal := false
		if want.Kind() == reflect.Func || want.Kind() == reflect.Pointer {
			equal = want.Pointer() == got.Pointer()
		} else {
			equal = reflect.DeepEqual(want.Interface(), got.Interface())
		}
		if !equal {
			t.Errorf("option %s lost in conversion", name)
		}
	}
	for i := 0; i < to.NumField(); i++ {
		name := to.Type().Field(i).Name
		if !from.FieldByName(name).IsValid() && executionOnly[name] == "" {
			t.Errorf("worker option %s has no postgate field or execution-only reason", name)
		}
	}
	for name := range executionOnly {
		if !to.FieldByName(name).IsValid() || from.FieldByName(name).IsValid() {
			t.Errorf("stale execution-only exception %s", name)
		}
	}
}
