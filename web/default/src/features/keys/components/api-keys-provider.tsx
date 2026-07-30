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
import React, { useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import useDialogState from '@/hooks/use-dialog'
import { useStatus } from '@/hooks/use-status'

import { rotateApiKey } from '../api'
import { ERROR_MESSAGES } from '../constants'
import { isTokenUsageSourceCapabilityEnabled } from '../lib/token-usage-source-capability'
import type { ApiKey, ApiKeysDialogType, OneTimeApiKeySecret } from '../types'

type ApiKeysContextType = {
  open: ApiKeysDialogType | null
  setOpen: (str: ApiKeysDialogType | null) => void
  currentRow: ApiKey | null
  setCurrentRow: React.Dispatch<React.SetStateAction<ApiKey | null>>
  refreshTrigger: number
  triggerRefresh: () => void
  oneTimeSecrets: OneTimeApiKeySecret[]
  setOneTimeSecrets: React.Dispatch<React.SetStateAction<OneTimeApiKeySecret[]>>
  withVerification: (
    apiCall: () => Promise<unknown>,
    config?: { title?: string; description?: string }
  ) => Promise<unknown>
  requestRotate: (apiKey: ApiKey) => Promise<void>
  usageSourcesEnabled: boolean
}

const ApiKeysContext = React.createContext<ApiKeysContextType | null>(null)

export function ApiKeysProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const [open, setOpen] = useDialogState<ApiKeysDialogType>(null)
  const [currentRow, setCurrentRow] = useState<ApiKey | null>(null)
  const [refreshTrigger, setRefreshTrigger] = useState(0)
  const [oneTimeSecrets, setOneTimeSecrets] = useState<OneTimeApiKeySecret[]>(
    []
  )
  const { status, isPlaceholderData } = useStatus()
  const usageSourcesEnabled = isTokenUsageSourceCapabilityEnabled(
    status,
    isPlaceholderData
  )
  const {
    open: verificationOpen,
    methods: verificationMethods,
    state: verificationState,
    executeVerification,
    withVerification,
    cancel: cancelVerification,
    setCode: setVerificationCode,
    switchMethod: switchVerificationMethod,
  } = useSecureVerification()

  const triggerRefresh = useCallback(() => {
    setRefreshTrigger((prev) => prev + 1)
  }, [])

  const requestRotate = useCallback(
    async (apiKey: ApiKey) => {
      try {
        await withVerification(
          async () => {
            const result = await rotateApiKey(apiKey.id)
            if (!result.success || !result.data?.key) {
              throw new Error(result.message || t(ERROR_MESSAGES.UNEXPECTED))
            }
            setOneTimeSecrets([
              {
                id: result.data.id,
                name: apiKey.name,
                key: `sk-${result.data.key}`,
              },
            ])
            setOpen('secret')
            triggerRefresh()
            return result
          },
          {
            title: t('Rotate API Key'),
            description: t(
              'Confirm your identity before replacing this API key.'
            ),
          }
        )
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : t(ERROR_MESSAGES.UNEXPECTED)
        )
      }
    },
    [setOpen, t, triggerRefresh, withVerification]
  )

  return (
    <ApiKeysContext
      value={{
        open,
        setOpen,
        currentRow,
        setCurrentRow,
        refreshTrigger,
        triggerRefresh,
        oneTimeSecrets,
        setOneTimeSecrets,
        withVerification,
        requestRotate,
        usageSourcesEnabled,
      }}
    >
      {children}
      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) cancelVerification()
        }}
        methods={verificationMethods}
        state={verificationState}
        onVerify={async (method, code) => {
          await executeVerification(method, code)
        }}
        onCancel={cancelVerification}
        onCodeChange={setVerificationCode}
        onMethodChange={switchVerificationMethod}
      />
    </ApiKeysContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useApiKeys = () => {
  const apiKeysContext = React.useContext(ApiKeysContext)

  if (!apiKeysContext) {
    throw new Error('useApiKeys has to be used within <ApiKeysContext>')
  }

  return apiKeysContext
}
