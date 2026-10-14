"use strict";
const { nativePaths, recordSignedNative } = require("./native-integrity.cjs");

module.exports = async context => {
  const { source, packaged, receipt } = nativePaths(context);
  // macOS signing changes the embedded Mach-O bytes after the copy was checked.
  recordSignedNative(source, packaged, receipt);
};
