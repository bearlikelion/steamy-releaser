import assert from "node:assert/strict";
import test from "node:test";
import { verifyWindowsRuntime } from "./verify-windows-runtime.mjs";

const info = [
  "binary.exe: go1.27.3",
  "\tbuild\t-tags=desktop,wv2runtime.browser,production",
  "\tbuild\tGOOS=windows",
  "\tbuild\tGOARCH=amd64",
].join("\n");
const safe = Buffer.from(
  "https://developer.microsoft.com/en-us/microsoft-edge/webview2/",
);
const bootstrapper = Buffer.from("mock embedded Microsoft installer");

test("accepts the browser strategy with no installer code or embedded bootstrapper", () => {
  assert.doesNotThrow(() => verifyWindowsRuntime(info, safe, bootstrapper));
});

test("rejects incorrect or ambiguous platform and strategy metadata", () => {
  for (const invalid of [
    info.replace("GOOS=windows", "GOOS=linux"),
    info.replace("GOARCH=amd64", "GOARCH=arm64"),
    info.replace("wv2runtime.browser", "wv2runtime.download"),
    info.replace("wv2runtime.browser", "wv2runtime.embed"),
    info + "\n\tbuild\t-tags=wv2runtime.download",
  ]) {
    assert.throws(() => verifyWindowsRuntime(invalid, safe, bootstrapper));
  }
});

test("rejects executable bytes from automatic or embedded installer paths", () => {
  for (const unsafe of [
    Buffer.from("not the official browser page"),
    Buffer.concat([safe, Buffer.from("webview2runtime.downloadBootstrapper")]),
    Buffer.concat([
      safe,
      Buffer.from("webview2runtime.InstallUsingBootstrapper"),
    ]),
    Buffer.concat([
      safe,
      Buffer.from("webview2runtime.InstallUsingEmbeddedBootstrapper"),
    ]),
    Buffer.concat([
      safe,
      Buffer.from("https://go.microsoft.com/fwlink/p/?LinkId=2124703"),
    ]),
    Buffer.concat([safe, bootstrapper]),
  ]) {
    assert.throws(() => verifyWindowsRuntime(info, unsafe, bootstrapper));
  }
});
