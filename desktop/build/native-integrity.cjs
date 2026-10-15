"use strict";

const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");

function sha256(file) {
  return crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
}

function recordCopiedNative(source, packaged, receipt) {
  const sourceSha256 = sha256(source);
  if (sourceSha256 !== sha256(packaged)) throw new Error("Native client source/package mismatch before signing");
  fs.writeFileSync(receipt, JSON.stringify({ sourceSha256, packagedSha256: sourceSha256 }) + "\n");
}

function recordSignedNative(source, packaged, receipt) {
  const record = JSON.parse(fs.readFileSync(receipt, "utf8"));
  if (sha256(source) !== record.sourceSha256) throw new Error("Native client source changed during signing");
  record.packagedSha256 = sha256(packaged);
  fs.writeFileSync(receipt, JSON.stringify(record) + "\n");
}

function verifyNativeIntegrity(source, packaged, receipt) {
  const record = JSON.parse(fs.readFileSync(receipt, "utf8"));
  if (sha256(source) !== record.sourceSha256 || sha256(packaged) !== record.packagedSha256) {
    throw new Error("Native client source/package integrity mismatch");
  }
}

function nativePaths(context) {
  const mac = context.electronPlatformName === "darwin";
  const filename = context.electronPlatformName === "win32" ? "penguin-remote.exe" : "penguin-remote";
  const resources = mac ? path.join(context.appOutDir, "Penguin Remote.app", "Contents", "Resources") :
    path.join(context.appOutDir, "resources");
  return {
    source: path.join(context.packager.projectDir, "native", filename),
    packaged: path.join(resources, "bin", filename),
    receipt: path.join(context.appOutDir, ".native-integrity.json"),
  };
}

module.exports = { recordCopiedNative, recordSignedNative, verifyNativeIntegrity, nativePaths };
