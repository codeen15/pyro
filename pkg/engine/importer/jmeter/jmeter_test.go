package jmeter

import (
	"os"
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/model"
)

func find(b *importer.Bundle, p string) *model.Request {
	for _, r := range b.Requests {
		if r.Path == p {
			return r.Request
		}
	}
	return nil
}

func TestJMeterImport(t *testing.T) {
	data, _ := os.ReadFile("testdata/shop.jmx")
	if !Looks(data) {
		t.Fatal("Looks")
	}
	b, err := Parse("", data)
	if err != nil {
		t.Fatal(err)
	}
	if b.Collection.Name != "Shop checkout" || b.Report.Requests != 6 || b.Report.Folders != 2 {
		t.Fatalf("%+v", b.Report)
	}
	vars := map[string]string{}
	for _, v := range b.Collection.Variables {
		vars[v.Key] = v.Value
	}
	if vars["baseUrl"] != "https://shop.test:8443" || vars["sku"] != "A-1" {
		t.Errorf("variables %v", vars)
	}
	login := find(b, "shoppers/log-in")
	if login == nil || login.URL != "{{baseUrl}}/api/login" || login.Body.Type != model.BodyJSON ||
		login.Body.Content != `{"user":"{{user}}","password":"{{pass}}"}` || len(login.Post) != 1 || login.Post[0].Extract == nil || login.Post[0].Extract.Var != "token" {
		t.Fatalf("login %+v", login)
	}
	hdr := map[string]string{}
	for _, h := range login.Headers {
		hdr[h.Key] = h.Value
	}
	if hdr["Accept"] != "application/json" || hdr["Content-Type"] != "application/json" {
		t.Errorf("headers (plan + sampler scope) %v", hdr)
	}
	list := find(b, "shoppers/list-products")
	// Post-processors in JMeter order: extractors, then assertions.
	post := list.Post
	if list.Query[0].Value != "{{$randomInt}}" || len(post) != 3 || post[0].Extract == nil || post[0].Extract.From != "regex" ||
		post[1].Assert == nil || post[1].Assert.Type != "status" || post[2].Assert.Op != "exists" {
		t.Errorf("list %+v %+v", list.Query, post)
	}
	// The group's constant timer and the sampler's Gaussian timer add up.
	if th := list.Think; th == nil || th.Type != model.TimerGaussian || th.Delay != "600ms" || th.Range != "50ms" {
		t.Errorf("list think %+v", list.Think)
	}
	if len(list.Pre) != 2 || list.Pre[0].Type != model.ProcVars || list.Pre[0].Vars[1].Value != "{{sku}}-eu" ||
		list.Pre[1].Counter == nil || list.Pre[1].Counter.Var != "cartNo" || list.Pre[1].Counter.End != 99 || list.Pre[1].Counter.Format != "%03d" {
		t.Errorf("list pre %+v", list.Pre)
	}
	add := find(b, "shoppers/add-to-cart")
	if add.URL != "{{baseUrl}}/api/cart/{{productId}}" || add.Body.Type != model.BodyForm || add.Headers[len(add.Headers)-1].Value != "Bearer {{token}}" {
		t.Errorf("add %+v", add)
	}
	if h := find(b, "health-checks/health"); h == nil || h.Post[0].Assert.Type != "duration" || h.Post[0].Assert.Op != "lte" || h.Think != nil {
		t.Errorf("health %+v", h)
	}
	if find(b, "health-checks/disabled") != nil {
		t.Error("disabled sampler imported")
	}

	// The load test: valid, with a scenario per thread group.
	plan, err := load.ParsePlan(b.LoadTests["shop-checkout"], nil)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, b.LoadTests["shop-checkout"])
	}
	shop := plan.Scenarios["shoppers"]
	if shop.Executor != load.RampingVUs || len(shop.Stages) != 2 || shop.Stages[0].Target != 20 || shop.Stages[1].Duration.D().Seconds() != 110 {
		t.Errorf("shoppers %+v", shop)
	}
	if shop.Data == nil || shop.Data.File != "data/users.csv" || b.DataFiles["data/users.csv"].Header != "user,pass" {
		t.Errorf("data %+v %+v", shop.Data, b.DataFiles)
	}
	flow := shop.Flow
	if len(flow) != 2 || !flow[0].Once || flow[0].Request != importer.CollectionRef+"shoppers/log-in" || flow[1].Group != "Browse and buy" {
		t.Fatalf("flow %+v", flow)
	}
	grp := flow[1].Steps
	// Timers live on the requests now, not as think steps.
	if grp[0].Request == "" || grp[1].If == nil ||
		grp[1].If.Expr != `productId != ""` || grp[1].If.Then[0].Loop.Count != 2 || len(grp[2].Weighted) != 2 {
		t.Errorf("group steps %+v", grp)
	}
	health := plan.Scenarios["health_checks"]
	if health.Executor != load.PerVUIterations || health.VUs != 2 || health.Iterations != 3 {
		t.Errorf("health %+v", health)
	}
	codes := map[string]bool{}
	for _, w := range b.Report.Warnings {
		codes[w.Code] = true
	}
	for _, c := range []string{"jmeter.script", "jmeter.csv", "jmeter.condition"} {
		if !codes[c] {
			t.Errorf("missing warning %s: %+v", c, b.Report.Warnings)
		}
	}
	if !strings.Contains(string(b.LoadTests["shop-checkout"]), "@collection/shoppers/add-to-cart") {
		t.Error("plan should reference requests through the collection placeholder")
	}
}

func TestJMeterConditions(t *testing.T) {
	c := &converter{b: &importer.Bundle{}}
	for in, want := range map[string]string{
		`${__jexl3("${status}" == "200")}`:      `status == 200`,
		`"${role}".equals("admin") && ${n} > 3`: `role == "admin" && n > 3`,
		`${__groovy(vars.get("x") == null,)}`:   `true`, // unconvertible → true, reported
	} {
		if got := c.condition(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}
