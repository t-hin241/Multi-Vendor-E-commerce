package eventbus

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The broker's permissions (deploy/nats/nats.conf) must follow the code: a
// user for every service on the bus, every durable consumer granted to the
// service that owns it, every event type publishable by its producer, and
// nothing broader. A new consumer or event type fails here until the
// configuration grants it (otherwise it would only fail at run time, with a
// permissions violation in the logs).

var (
	userBlock   = regexp.MustCompile(`(?s)user:\s*(\w+)(.*?)(?:\n\s*\}\n\s*\{|\n\s*\}\n\s*\]\n)`)
	quoted      = regexp.MustCompile(`"([^"]+)"`)
	durableDecl = regexp.MustCompile(`Durable:\s*"([^"]+)"`)
	eventType   = regexp.MustCompile(`=\s*"([a-z]+)\.([a-z_]+)"`)
)

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// grants maps each broker user to the subjects it may publish/subscribe.
func grants(t *testing.T) map[string][]string {
	t.Helper()
	conf := readRepoFile(t, "deploy", "nats", "nats.conf")
	users := map[string][]string{}
	for _, m := range userBlock.FindAllStringSubmatch(conf, -1) {
		for _, q := range quoted.FindAllStringSubmatch(m[2], -1) {
			users[m[1]] = append(users[m[1]], q[1])
		}
	}
	if len(users) == 0 {
		t.Fatal("no users parsed from nats.conf")
	}
	return users
}

// servicesOnBus maps each service that starts the event bus to its durables.
func servicesOnBus(t *testing.T) map[string][]string {
	t.Helper()
	mains, err := filepath.Glob(filepath.Join("..", "..", "services", "*", "cmd", "server", "main.go"))
	if err != nil || len(mains) == 0 {
		t.Fatalf("no service mains found: %v", err)
	}
	out := map[string][]string{}
	for _, path := range mains {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "eventbus.Start(") {
			continue
		}
		service := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
		out[service] = []string{}
		for _, d := range durableDecl.FindAllStringSubmatch(src, -1) {
			out[service] = append(out[service], d[1])
		}
	}
	return out
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestBrokerPermissionsMatchTheCode(t *testing.T) {
	users := grants(t)
	services := servicesOnBus(t)

	var names []string
	for s := range services {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		if _, ok := users[s]; !ok {
			t.Errorf("service %s uses the event bus but has no broker user", s)
		}
		for _, d := range services[s] {
			if !strings.HasPrefix(d, s+"-") {
				t.Errorf("durable %s of %s must be named %s-...", d, s, s)
			}
			for _, subject := range []string{
				"$JS.API.CONSUMER.CREATE.SHOPEE_EVENTS." + d,
				"$JS.API.CONSUMER.MSG.NEXT.SHOPEE_EVENTS." + d,
				"$JS.ACK.SHOPEE_EVENTS." + d + ".>",
			} {
				if !has(users[s], subject) {
					t.Errorf("%s lacks %s", s, subject)
				}
			}
		}
	}
	for u := range users {
		if _, ok := services[u]; !ok {
			t.Errorf("broker user %s is not a service on the event bus", u)
		}
	}

	// Each event type is published by the service its name starts with.
	for _, m := range eventType.FindAllStringSubmatch(readRepoFile(t, "backend", "pkg", "events", "events.go"), -1) {
		if !has(users[m[1]], "shopee.events."+m[1]+".>") {
			t.Errorf("event %s.%s: %s may not publish it", m[1], m[2], m[1])
		}
	}

	// Nothing broader than a user's own share.
	for u, subjects := range users {
		for _, s := range subjects {
			switch {
			case strings.HasPrefix(s, "shopee.events."):
				if s != "shopee.events."+u+".>" {
					t.Errorf("%s may publish %s (only shopee.events.%s.>)", u, s, u)
				}
			case strings.HasPrefix(s, "_INBOX"):
				if s != "_INBOX_"+u+".>" {
					t.Errorf("%s may subscribe to %s", u, s)
				}
			case strings.HasPrefix(s, "$JS.API.STREAM."):
				if !has([]string{"$JS.API.STREAM.INFO.SHOPEE_EVENTS", "$JS.API.STREAM.CREATE.SHOPEE_EVENTS", "$JS.API.STREAM.UPDATE.SHOPEE_EVENTS"}, s) {
					t.Errorf("%s may call %s", u, s)
				}
			case strings.HasPrefix(s, "$JS.API.CONSUMER."), strings.HasPrefix(s, "$JS.ACK."):
				if !strings.Contains(s, ".SHOPEE_EVENTS."+u+"-") {
					t.Errorf("%s may use another service's consumer: %s", u, s)
				}
			default:
				t.Errorf("%s has an unexpected grant %s", u, s)
			}
		}
	}
}
