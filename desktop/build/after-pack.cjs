"use strict";
const { nativePaths, recordCopiedNative } = require("./native-integrity.cjs");

module.exports = async context => {
  const { source, packaged, receipt } = nativePaths(context);
  recordCopiedNative(source, packaged, receipt);
};
