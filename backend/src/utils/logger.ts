type Level = 'info' | 'warn' | 'error' | 'debug';

function log(level: Level, context: string, message: string, data?: unknown): void {
  const ts = new Date().toISOString();
  const prefix = `[${ts}] [${level.toUpperCase()}] [${context}]`;
  if (data !== undefined) {
    console[level === 'debug' ? 'log' : level](`${prefix} ${message}`, data);
  } else {
    console[level === 'debug' ? 'log' : level](`${prefix} ${message}`);
  }
}

export function createLogger(context: string) {
  return {
    info:  (msg: string, data?: unknown) => log('info',  context, msg, data),
    warn:  (msg: string, data?: unknown) => log('warn',  context, msg, data),
    error: (msg: string, data?: unknown) => log('error', context, msg, data),
    debug: (msg: string, data?: unknown) => log('debug', context, msg, data),
  };
}
