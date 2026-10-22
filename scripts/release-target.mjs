export const productionGoUrl = "https://go.penguin.ooo";
export const stagingGoUrl = "https://107-173-122-149.sslip.io";

export function releaseVersion(tag) {
  if (typeof tag !== "string" || !/^desktop-v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(tag) || tag.includes("..")) {
    throw new Error("Expected a safe complete desktop-v<version> tag");
  }
  const version = tag.slice("desktop-v".length);
  releaseTarget(version);
  return version;
}

export function releaseTarget(version) {
  if (typeof version !== "string" || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error("Invalid package version");
  }
  const staging = /^\d+\.\d+\.\d+-staging\.(?:0|[1-9]\d*)$/.test(version);
  if (/staging/i.test(version) && !staging) throw new Error("Invalid staging release version");
  const prerelease = version.includes("-");
  return {
    channel: staging ? "staging" : "production",
    penguinGoUrl: staging ? stagingGoUrl : productionGoUrl,
    prerelease,
    makeLatest: !prerelease,
  };
}
