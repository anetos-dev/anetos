// The API's client: createApi comes from src/types/api.ts, which
// `go run . types` writes from the app's Go types (anetos dev keeps it up
// to date), a function per route of /api/v1. The SPA's origin is in
// HTTP_SPA_ORIGINS, so the API logs it in with a session cookie, and the
// client sends the XSRF-TOKEN cookie back in a header.
// region: client
import { ApiError, createApi } from './types/api'

export const api = createApi().api

// errorMessages says what went wrong: a 422's message per field of the
// input ({ email: "…" }), otherwise the error's message, under "".
export function errorMessages(err: unknown): Record<string, string> {
  if (err instanceof ApiError) {
    return err.problem.errors ?? { '': err.message }
  }
  return { '': err instanceof Error ? err.message : String(err) }
}
// endregion

// errorText is errorMessages' messages, in a sentence or a few.
export function errorText(err: unknown): string {
  return Object.values(errorMessages(err)).join(' ')
}

// isStatus reports whether err is the API's answer with that status.
export function isStatus(err: unknown, status: number): boolean {
  return err instanceof ApiError && err.status === status
}

// tokenAnswer is the error of a login answered with a token: the page's
// origin isn't in HTTP_SPA_ORIGINS, so the API took it for another client.
export const tokenAnswer = "The API answered with a token: list this page's origin in HTTP_SPA_ORIGINS"
