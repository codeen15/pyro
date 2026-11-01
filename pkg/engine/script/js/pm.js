// Postman-compatible scripting API (pm.*), built on host functions that the
// Go runtime installs as __host. Loaded once per runtime after chai.
// See docs/postman-import.md for the supported surface.
(function (global) {
  "use strict";
  var host = global.__host;
  var chai = global.chai;

  function str(v) {
    if (v === undefined || v === null) return "";
    if (typeof v === "string") return v;
    if (typeof v === "object") return JSON.stringify(v);
    return String(v);
  }

  // --- Variable scopes ------------------------------------------------------

  function scope(name) {
    return {
      get: function (k) {
        var v = host.varGet(name, String(k));
        return v === null ? undefined : v;
      },
      set: function (k, v) {
        host.varSet(name, String(k), str(v));
      },
      unset: function (k) {
        host.varUnset(name, String(k));
      },
      has: function (k) {
        return host.varGet(name, String(k)) !== null;
      },
      clear: function () {
        host.varClear(name);
      },
      toObject: function () {
        return host.varAll(name);
      },
      replaceIn: function (s) {
        return host.replaceIn(str(s));
      },
    };
  }

  var variables = scope("variables");
  var environment = scope("environment");
  environment.name = host.envName();

  // --- Request (pre-request scripts may change it) --------------------------

  function headerList(arr) {
    var list = {
      _items: arr,
      add: function (h) {
        arr.push({ key: String(h.key), value: str(h.value) });
      },
      upsert: function (h) {
        var k = String(h.key).toLowerCase();
        for (var i = 0; i < arr.length; i++) {
          if (arr[i].key.toLowerCase() === k) {
            arr[i].value = str(h.value);
            arr[i].disabled = false;
            return;
          }
        }
        list.add(h);
      },
      remove: function (key) {
        var k = String(key).toLowerCase();
        for (var i = arr.length - 1; i >= 0; i--) if (arr[i].key.toLowerCase() === k) arr.splice(i, 1);
      },
      get: function (key) {
        var k = String(key).toLowerCase();
        for (var i = 0; i < arr.length; i++) if (!arr[i].disabled && arr[i].key.toLowerCase() === k) return arr[i].value;
        return undefined;
      },
      has: function (key) {
        return list.get(key) !== undefined;
      },
      each: function (fn) {
        arr.filter(function (h) { return !h.disabled; }).forEach(fn);
      },
      toObject: function () {
        var o = {};
        list.each(function (h) { o[h.key] = h.value; });
        return o;
      },
      all: function () {
        return arr.slice();
      },
    };
    return list;
  }

  var reqData = JSON.parse(host.requestJSON());
  reqData.headers = reqData.headers || [];
  var request = {
    get url() {
      return { toString: function () { return reqData.url; } , raw: reqData.url };
    },
    set url(v) {
      reqData.url = str(v);
    },
    get method() {
      return reqData.method || "GET";
    },
    set method(v) {
      reqData.method = String(v).toUpperCase();
    },
    headers: headerList(reqData.headers),
    addHeader: function (h) { request.headers.add(h); },
    removeHeader: function (k) { request.headers.remove(k); },
    body: {
      get mode() { return reqData.body ? reqData.body.type : "none"; },
      get raw() { return reqData.body ? reqData.body.content || "" : ""; },
      set raw(v) {
        reqData.body = reqData.body || { type: "text" };
        reqData.body.content = str(v);
      },
      update: function (v) {
        if (typeof v === "string") request.body.raw = v;
        else if (v && v.raw !== undefined) request.body.raw = v.raw;
      },
      toString: function () { return request.body.raw; },
    },
  };
  global.__pyroRequestOut = function () {
    return JSON.stringify(reqData);
  };

  // --- Response (post-response scripts only) --------------------------------

  function makeResponse(r) {
    if (!r) return undefined;
    var headers = headerList(r.headers || []);
    var parsed;
    var res = {
      code: r.code,
      status: r.status,
      headers: headers,
      responseTime: r.responseTime,
      responseSize: r.responseSize,
      text: function () { return r.body; },
      json: function () {
        if (parsed === undefined) parsed = JSON.parse(r.body);
        return parsed;
      },
      reason: function () { return r.status; },
    };
    function fail(msg) {
      throw new chai.AssertionError(msg);
    }
    function expectStatus(n) {
      if (typeof n === "string") {
        if (res.status !== n) fail("expected response to have status reason '" + n + "' but got '" + res.status + "'");
      } else if (res.code !== n) {
        fail("expected response to have status code " + n + " but got " + res.code);
      }
    }
    function between(lo, hi, label) {
      return function () {
        if (res.code < lo || res.code > hi) fail("expected response to be " + label + " but got " + res.code);
      };
    }
    var have = {
      status: expectStatus,
      header: function (k, v) {
        if (!headers.has(k)) fail("expected response to have header " + k);
        if (v !== undefined && headers.get(k) !== v) fail("expected header " + k + " to be '" + v + "' but got '" + headers.get(k) + "'");
      },
      body: function (b) {
        if (b === undefined) {
          if (!r.body) fail("expected response to have a body");
        } else if (b instanceof RegExp ? !b.test(r.body) : r.body !== b) {
          fail("expected response body to match");
        }
      },
      jsonBody: function (path, value) {
        var data;
        try { data = res.json(); } catch (e) { fail("expected response body to be valid JSON"); }
        if (path === undefined) return;
        if (typeof path === "object") { chai.expect(data).to.deep.include(path); return; }
        var cur = data;
        var parts = String(path).split(".");
        for (var i = 0; i < parts.length; i++) {
          if (cur === null || typeof cur !== "object" || !(parts[i] in cur)) fail("expected response to have JSON property '" + path + "'");
          cur = cur[parts[i]];
        }
        if (arguments.length > 1) chai.expect(cur).to.deep.equal(value);
      },
      responseTime: {
        below: function (ms) { if (!(res.responseTime < ms)) fail("expected response time below " + ms + " ms but got " + res.responseTime); },
        above: function (ms) { if (!(res.responseTime > ms)) fail("expected response time above " + ms + " ms but got " + res.responseTime); },
      },
    };
    var be = {
      get ok() { between(200, 299, "ok")(); return true; },
      get success() { between(200, 299, "successful")(); return true; },
      get info() { between(100, 199, "informational")(); return true; },
      get redirection() { between(300, 399, "a redirection")(); return true; },
      get clientError() { between(400, 499, "a client error")(); return true; },
      get serverError() { between(500, 599, "a server error")(); return true; },
      get error() { between(400, 599, "an error")(); return true; },
      get notFound() { expectStatus(404); return true; },
      get unauthorized() { expectStatus(401); return true; },
      get forbidden() { expectStatus(403); return true; },
      get badRequest() { expectStatus(400); return true; },
      get accepted() { expectStatus(202); return true; },
      get withBody() { have.body(); return true; },
      get json() { have.jsonBody(); return true; },
    };
    res.to = { have: have, be: be, not: { be: { get error() { if (res.code >= 400) fail("expected response not to be an error but got " + res.code); return true; } } } };
    return res;
  }

  var respData = host.responseJSON();
  var response = respData ? makeResponse(JSON.parse(respData)) : undefined;

  // --- Tests, logging, control flow -----------------------------------------

  function test(name, fn) {
    try {
      if (typeof fn === "function") fn();
      host.test(String(name), true, "");
    } catch (e) {
      host.test(String(name), false, e && e.message ? e.message : String(e));
    }
  }
  test.skip = function (name) {
    host.log("info", "skipped test: " + name);
  };

  function sendRequest(req, cb) {
    var spec = typeof req === "string" ? { url: req } : req;
    var out;
    try {
      out = JSON.parse(host.sendRequest(JSON.stringify(spec)));
    } catch (e) {
      if (cb) cb(e, null);
      else throw e;
      return;
    }
    var res = makeResponse(out);
    if (cb) cb(null, res);
    return res;
  }

  var cookies = {
    get: function (name) {
      var c = (respData && JSON.parse(respData).cookies) || [];
      for (var i = 0; i < c.length; i++) if (c[i].name === name) return c[i].value;
      return undefined;
    },
    has: function (name) { return cookies.get(name) !== undefined; },
    toObject: function () {
      var o = {};
      ((respData && JSON.parse(respData).cookies) || []).forEach(function (c) { o[c.name] = c.value; });
      return o;
    },
  };

  var info = JSON.parse(host.infoJSON());

  global.pm = {
    info: info,
    variables: variables,
    environment: environment,
    collectionVariables: scope("collection"),
    globals: scope("globals"),
    iterationData: scope("data"),
    request: request,
    response: response,
    cookies: cookies,
    test: test,
    expect: chai.expect,
    sendRequest: sendRequest,
    execution: {
      skipRequest: function () { host.skip(); },
      setNextRequest: function (n) { host.next(n === null ? null : String(n)); },
      location: info.location || [],
    },
    visualizer: {
      set: function () { host.log("warn", "pm.visualizer is not supported in Pyro; ignored"); },
    },
  };

  // Legacy Postman sandbox globals.
  global.postman = {
    setNextRequest: function (n) { host.next(n === null ? null : String(n)); },
    setEnvironmentVariable: environment.set,
    getEnvironmentVariable: environment.get,
    clearEnvironmentVariable: environment.unset,
    setGlobalVariable: global.pm.globals.set,
    getGlobalVariable: global.pm.globals.get,
  };
  if (response) {
    global.responseBody = response.text();
    global.responseCode = { code: response.code, name: response.status };
    global.responseTime = response.responseTime;
    global.responseHeaders = response.headers.toObject();
  }
  global.tests = {};
  global.__pyroLegacyTests = function () {
    Object.keys(global.tests).forEach(function (k) {
      host.test(k, !!global.tests[k], global.tests[k] ? "" : "test evaluated to false");
    });
  };

  var log = function (level) {
    return function () {
      var parts = [];
      for (var i = 0; i < arguments.length; i++) parts.push(typeof arguments[i] === "string" ? arguments[i] : JSON.stringify(arguments[i]));
      host.log(level, parts.join(" "));
    };
  };
  global.console = { log: log("log"), info: log("info"), warn: log("warn"), error: log("error"), debug: log("debug") };

  global.require = function (name) {
    if (name === "chai") return chai;
    if (name === "uuid") return { v4: host.uuid };
    throw new Error("require('" + name + "') is not available in Pyro yet");
  };
})(this);
