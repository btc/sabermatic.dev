export class ApiError extends Error {
  constructor(
    public status: number,
    public body: unknown,
  ) {
    super(`API error ${status}`);
    this.name = "ApiError";
  }
}

function getCsrfToken(): string {
  const match = document.cookie.match(/drill_csrf=([^;]+)/);
  return match?.[1] ?? "";
}

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(url, {
    credentials: "same-origin",
    ...options,
  });
  if (response.status === 204) {
    return null as T;
  }
  let data: unknown;
  try {
    data = await response.json();
  } catch {
    const text = await response.text().catch(() => "");
    throw new ApiError(response.status, text || `Non-JSON response (${response.status})`);
  }
  if (!response.ok) {
    throw new ApiError(response.status, data);
  }
  return data as T;
}

function mutationHeaders(body?: unknown): HeadersInit {
  const headers: Record<string, string> = {
    "X-CSRF-Token": getCsrfToken(),
  };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  return headers;
}

export const apiClient = {
  get<T>(url: string): Promise<T> {
    return request<T>(url, { method: "GET" });
  },
  post<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "POST",
      headers: mutationHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  put<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "PUT",
      headers: mutationHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  patch<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "PATCH",
      headers: mutationHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  delete<T>(url: string): Promise<T> {
    return request<T>(url, {
      method: "DELETE",
      headers: mutationHeaders(),
    });
  },
};
