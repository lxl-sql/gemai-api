import { describe, expect, test } from 'vitest'

import { extractVerificationInfo } from './secure-verification'

describe('secure verification challenge', () => {
  test('preserves the challenge returned by the protected API', () => {
    const info = extractVerificationInfo({
      response: {
        data: {
          code: 'VERIFICATION_REQUIRED',
          message: 'Secure verification is required',
          verification_challenge: 'signed-challenge',
        },
      },
    })

    expect(info).toEqual({
      code: 'VERIFICATION_REQUIRED',
      challenge: 'signed-challenge',
      message: 'Secure verification is required',
      required: true,
    })
  })
})
