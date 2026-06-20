/** Minimal ambient types for autocannon (no @types package published). */
declare module 'autocannon' {
  interface Result {
    latency: { p50: number; p99: number; average: number };
    requests: { average: number };
  }
  interface Options {
    url: string;
    connections?: number;
    duration?: number;
  }
  function autocannon(opts: Options): Promise<Result>;
  namespace autocannon {
    function printResult(result: Result): string;
  }
  export = autocannon;
}
