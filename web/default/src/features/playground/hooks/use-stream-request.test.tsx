/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { ERROR_MESSAGES } from '../constants'
import { useStreamRequest } from './use-stream-request'

type StreamListener = (
  event: Event & { data?: string; readyState?: number }
) => void | Promise<void>

type MockSSEInstance = {
  readyState: number
  status: number
  close: ReturnType<typeof vi.fn>
  options: Record<string, unknown>
  stream: ReturnType<typeof vi.fn>
  emit: (
    type: string,
    event: Event & { data?: string; readyState?: number }
  ) => void
}

const sseInstances = vi.hoisted(() => [] as MockSSEInstance[])
const sseMockErrors = vi.hoisted(() => ({
  constructor: null as Error | null,
  stream: null as Error | null,
}))

vi.mock('sse.js', () => {
  class MockSSE {
    readyState = 1
    status = 200
    options: Record<string, unknown>
    close = vi.fn(() => {
      this.readyState = 2
      this.emit('readystatechange', {
        readyState: 2,
      } as Event & { readyState: number })
    })
    stream = vi.fn(() => {
      if (sseMockErrors.stream) throw sseMockErrors.stream
    })
    private listeners = new Map<string, StreamListener[]>()

    constructor(_url: string, options: Record<string, unknown>) {
      if (sseMockErrors.constructor) throw sseMockErrors.constructor
      this.options = options
      sseInstances.push(this)
    }

    addEventListener(type: string, listener: StreamListener) {
      const listeners = this.listeners.get(type) ?? []
      listeners.push(listener)
      this.listeners.set(type, listeners)
    }

    emit(type: string, event: Event & { data?: string; readyState?: number }) {
      for (const listener of this.listeners.get(type) ?? []) listener(event)
    }
  }

  return { SSE: MockSSE }
})

const payload = {
  model: 'test-model',
  messages: [],
  stream: true,
}

beforeEach(() => {
  sseInstances.length = 0
  sseMockErrors.constructor = null
  sseMockErrors.stream = null
})

describe('useStreamRequest', () => {
  test('attaches listeners before explicitly starting the stream', () => {
    const { result } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), vi.fn())
    })

    const source = sseInstances[0]
    expect(source.options.start).toBe(false)
    expect(source.stream).toHaveBeenCalledOnce()
  })

  test('reports a synchronous stream construction failure', () => {
    const onError = vi.fn()
    sseMockErrors.constructor = new Error('constructor failed')
    const { result } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), onError)
    })

    expect(onError).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledWith(ERROR_MESSAGES.STREAM_START_ERROR)
    expect(result.current.isStreaming).toBe(false)
  })

  test('settles a stream that throws while starting', () => {
    const onError = vi.fn()
    sseMockErrors.stream = new Error('stream failed')
    const { result } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), onError)
    })

    const source = sseInstances[0]
    expect(onError).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledWith(
      ERROR_MESSAGES.STREAM_START_ERROR,
      undefined
    )
    expect(source.close).toHaveBeenCalledOnce()
    expect(result.current.isStreaming).toBe(false)
  })

  test('closes the active stream when the page unmounts', () => {
    const { result, unmount } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), vi.fn())
    })

    const source = sseInstances[0]
    expect(source).toBeDefined()

    unmount()

    expect(source.close).toHaveBeenCalledOnce()
  })

  test('reports a terminal stream error only once', () => {
    const onError = vi.fn()
    const { result } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), onError)
    })

    const source = sseInstances[0]
    source.status = 500

    act(() => {
      source.emit('readystatechange', {
        readyState: 2,
      } as Event & { readyState: number })
      source.emit('error', {
        data: JSON.stringify({ error: { message: 'upstream failed' } }),
      } as Event & { data: string })
    })

    expect(onError).toHaveBeenCalledOnce()
    expect(source.close).toHaveBeenCalledOnce()
  })

  test('reports an error when a successful HTTP stream closes before done', () => {
    const onComplete = vi.fn()
    const onError = vi.fn()
    const { result } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), onComplete, onError)
    })

    const source = sseInstances[0]

    act(() => {
      source.emit('readystatechange', {
        readyState: 2,
      } as Event & { readyState: number })
    })

    expect(onComplete).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledWith(
      ERROR_MESSAGES.CONNECTION_CLOSED,
      undefined
    )
    expect(source.close).toHaveBeenCalledOnce()
    expect(result.current.isStreaming).toBe(false)
  })

  test('does not report an error when the user stops the stream', () => {
    const onError = vi.fn()
    const { result } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), onError)
      result.current.stopStream()
    })

    const source = sseInstances[0]
    expect(onError).not.toHaveBeenCalled()
    expect(source.close).toHaveBeenCalledOnce()
    expect(result.current.isStreaming).toBe(false)
  })

  test('ignores late events from a replaced stream', () => {
    const onFirstError = vi.fn()
    const onSecondError = vi.fn()
    const { result } = renderHook(() => useStreamRequest())

    act(() => {
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), onFirstError)
      result.current.sendStreamRequest(payload, vi.fn(), vi.fn(), onSecondError)
    })

    const firstSource = sseInstances[0]
    const secondSource = sseInstances[1]

    act(() => {
      firstSource.emit('error', {
        data: JSON.stringify({ error: { message: 'late failure' } }),
      } as Event & { data: string })
    })

    expect(firstSource.close).toHaveBeenCalledOnce()
    expect(secondSource.close).not.toHaveBeenCalled()
    expect(onFirstError).not.toHaveBeenCalled()
    expect(onSecondError).not.toHaveBeenCalled()
    expect(result.current.isStreaming).toBe(true)
  })
})
