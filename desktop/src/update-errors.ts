/** Signature invalidation removes the cached installer; recovery starts with a new check. */
export class WindowsUpdateVerificationError extends Error {
  constructor() {
    super("Windows update verification failed. Check for updates and download it again.");
    this.name = "WindowsUpdateVerificationError";
  }
}
