package interp

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestFS(t *testing.T) {
	dir := t.TempDir()
	expect(t, "files and directories", fmt.Sprintf(`
import fs
let dir = %q
let sub = fs::join(dir, "notes", "2026")
print(fs::mkdir(sub).is_ok(), fs::is_dir(sub))
let path = fs::join(sub, "todo.txt")
fs::write(path, "one\n").unwrap()
fs::append(path, "two\n").unwrap()
let text = fs::read(path).unwrap()
print(text.lines(), fs::exists(path), fs::list(sub).unwrap())
print(fs::read(fs::join(dir, "missing.txt")).is_err(), fs::remove(path).is_ok(), fs::exists(path))
match fs::read(dir):
    result::err(msg): print("error is a message:", type(msg))
    _: print("??")
`, dir), `true true
["one", "two"] true ["todo.txt"]
true true false
error is a message: str`)
}

func TestOS(t *testing.T) {
	t.Setenv("SEPL_TEST_VAR", "hello")
	expect(t, "env, run, platform", `
import os
print(os::env("SEPL_TEST_VAR"), os::env("SEPL_NO_SUCH_VAR"), type(os::platform), len(os::cwd()) > 0)
print(os::run("echo", "hi from echo").unwrap().trim())
print(os::run("false").is_err(), os::run("no-such-command-sepl").is_err())
`, "option::some(\"hello\") option::none str true\nhi from echo\ntrue true")
}

func TestJSON(t *testing.T) {
	expect(t, "parse and write", `
import json
let data = json::parse("{\"name\": \"SEPL\", \"tags\": [\"go\", \"vm\"], \"stars\": 5, \"rating\": 4.5, \"ok\": true, \"none\": null}").unwrap()
print(data["name"], data["tags"][1], data["stars"] + 1, data["rating"], data["ok"], data["none"], data.keys())
print(json::to_str(data))
print(json::to_str([1, {"a": [true, nil]}], indent: 2))
struct point:
    let x
    let y
enum color:
    red
print(json::to_str({"p": point(x: 1, y: 2.5), "c": color::red, "text": "quote \" and\nnewline <b>"}))
print(json::parse("[1, 2").is_err(), json::parse("{} extra").is_err(), json::parse("  [] ").unwrap())
`, `SEPL vm 6 4.5 true nil ["name", "tags", "stars", "rating", "ok", "none"]
{"name":"SEPL","tags":["go","vm"],"stars":5,"rating":4.5,"ok":true,"none":null}
[
  1,
  {
    "a": [
      true,
      null
    ]
  }
]
{"p":{"x":1,"y":2.5},"c":"red","text":"quote \" and\nnewline <b>"}
true true []`)

	expectErr(t, "json of a function", "import json\njson::to_str(print)", "runtime", 2, "json::to_str(): fn cannot be written as JSON")
	expectErr(t, "json of a self-referencing list", "import json\nlet l = []\nl.push(l)\njson::to_str(l)", "runtime", 4, "nested too deeply")
}

func TestRegex(t *testing.T) {
	expect(t, "re", `
import re
let text = "Order 66 shipped on 2026-10-08, order 67 on 2026-10-09"
print(re::matches("\\d{4}-\\d{2}", text), re::matches("^\\d+$", text))
print(re::find("\\d+", text), re::find("xyz", text))
print(re::find_all("\\d{4}-\\d{2}-\\d{2}", text))
print(re::groups("(\\d{4})-(\\d{2})", text).unwrap())
print(re::replace("(\\d{4})-(\\d{2})-(\\d{2})", text, "$3.$2.$1"))
print(re::split("\\s*,\\s*", "a , b,c"))
`, `true false
option::some("66") option::none
["2026-10-08", "2026-10-09"]
["2026-10", "2026", "10"]
Order 66 shipped on 08.10.2026, order 67 on 09.10.2026
["a", "b", "c"]`)

	expectErr(t, "bad pattern", "import re\nre::find(\"(\", \"x\")", "runtime", 2, "re::find(): bad pattern \"(\"")
}

func TestMathAndTime(t *testing.T) {
	expect(t, "math", `
import math
print(math::round(2.567, 2), math::round(2.5), math::round(-2.5), math::clamp(15, 0, 10), math::clamp(-1, 0, 10), math::clamp(5, 0, 10))
print(math::atan2(1, 1) * 4 == math::pi, math::hypot(3, 4), math::log10(1000), math::log2(8), math::is_nan(0.0))
`, "2.57 3 -3 10 0 5\ntrue 5.0 3.0 3.0 false")

	expect(t, "time format", `
import time
print(time::format(1700000000, "%Y-%m"), len(time::format()), time::format(0, "100%%"))
`, "2023-11 19 100%")
}

func TestCollectionMethods(t *testing.T) {
	expect(t, "list", `
fn double(x):
    return x * 2
fn is_even(x):
    return x % 2 == 0
fn add(a, b):
    return a + b
fn neg(x):
    return -x
let nums = [5, 2, 8, 1, 2]
print(nums.map(double), nums.filter(is_even), nums.reduce(add, 0), nums.sum(), [].sum(), [1.5, 2].sum())
print(nums.any(is_even), nums.all(is_even), nums.find(is_even), [1, 3].find(is_even), nums.count(2))
print(["a", "b"].enumerate())
nums.sort(neg)
print(nums)
let words = ["banana", "kiwi", "apple"]
words.sort(len)
print(words, {"a": 1, "b": 2}.items())
`, `[10, 4, 16, 2, 4] [2, 8, 2] 18 18 0 3.5
true false option::some(2) option::none 2
[[0, "a"], [1, "b"]]
[8, 5, 2, 2, 1]
["kiwi", "apple", "banana"] [["a", 1], ["b", 2]]`)

	expect(t, "str", `
print("a\nb\r\nc\n".lines(), "".lines(), "banana".count("an"))
print("7".pad_left(3, "0"), "ab".pad_right(4, ".") + "|", "long".pad_left(2))
print("123".is_digit(), "12a".is_digit(), "Привет".is_alpha(), " \t".is_space(), "".is_digit())
`, `["a", "b", "c"] [] 2
007 ab..| long
true false true true false`)

	expectErr(t, "callback errors keep their position", "fn bad(x):\n    return x + \"s\"\nprint([1].map(bad))", "runtime", 2,
		"cannot use '+' with int and str")
}

func TestStdlibExample(t *testing.T) {
	dir := writeFiles(t, map[string]string{"data.json": `{"users": [{"name": "Ann", "age": 31}, {"name": "Bob", "age": 17}]}`})
	src := readExample(t, "stdlib.sepl")
	var out strings.Builder
	s := NewSession(&out, strings.NewReader(""))
	code, err := s.RunFile(filepath.Join(dir, "stdlib.sepl"), src, []string{filepath.Join(dir, "data.json")})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v\n%s", code, err, formatErr(err))
	}
	if !strings.Contains(out.String(), "adults: Ann (31)") {
		t.Errorf("output:\n%s", out.String())
	}
}
