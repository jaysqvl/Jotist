// Both CLI settings views must tell the client installer which server supplied
// the script. Docker environment variables never determine a browser origin.
export function cliInstallCommand(origin: string): string {
    const url = new URL(origin);
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) throw new Error("Invalid Jotist server origin");
    const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
    return `curl -fsSL ${quote(`${url.origin}/install.sh`)} | bash -s -- ${quote(url.origin)}`;
}
