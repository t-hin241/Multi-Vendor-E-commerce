export default async function teardown() {
  const response = await fetch("http://localhost:3999/test/stop", {
    signal: AbortSignal.timeout(5_000),
    method: "POST",
    headers: { Origin: "http://localhost:3100", "X-CSRF-Protection": "1" },
  });
  if (!response.ok) throw new Error("AF-12 fixture shutdown failed");
}
