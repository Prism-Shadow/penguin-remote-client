export const productionGoUrl = "https://go.penguin.ooo";
export const stagingGoUrl = "https://107-173-122-149.sslip.io";

export function releaseTarget(version) {
  if (typeof version !== "string" || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error("Invalid package version");
  }
  const staging = /^\d+\.\d+\.\d+-staging\.(?:0|[1-9]\d*)$/.test(version);
  if (/staging/i.test(version) && !staging) throw new Error("Invalid staging release version");
  return { channel: staging ? "staging" : "production", penguinGoUrl: staging ? stagingGoUrl : productionGoUrl };
}
