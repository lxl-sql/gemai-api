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
import { DownloadIcon, UploadIcon } from 'lucide-react'
import { useRef, useState, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'

import {
  createPlaygroundBackup,
  parsePlaygroundBackup,
  type PlaygroundBackupData,
} from '../../lib'
import type { Message, ParameterEnabled, PlaygroundConfig } from '../../types'

const MAX_BACKUP_FILE_BYTES = 5 * 1024 * 1024

type PlaygroundBackupControlsProps = {
  config: PlaygroundConfig
  disabled?: boolean
  messages: Message[]
  onImport: (backup: PlaygroundBackupData) => void
  parameterEnabled: ParameterEnabled
}

export function PlaygroundBackupControls(props: PlaygroundBackupControlsProps) {
  const { t } = useTranslation()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [importCandidate, setImportCandidate] =
    useState<PlaygroundBackupData | null>(null)

  function handleExport(): void {
    const backup = createPlaygroundBackup({
      config: props.config,
      messages: props.messages,
      parameterEnabled: props.parameterEnabled,
    })
    const blob = new Blob([JSON.stringify(backup, null, 2)], {
      type: 'application/json',
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `playground-backup-${backup.exportedAt.slice(0, 10)}.json`
    link.click()
    URL.revokeObjectURL(url)
    toast.success(t('Playground backup exported'))
  }

  async function handleFileChange(
    event: ChangeEvent<HTMLInputElement>
  ): Promise<void> {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return

    if (file.size > MAX_BACKUP_FILE_BYTES) {
      toast.error(t('The playground backup file is too large.'))
      return
    }

    try {
      const parsed = JSON.parse(await file.text()) as unknown
      setImportCandidate(parsePlaygroundBackup(parsed))
    } catch {
      toast.error(t('Invalid playground backup file'))
    }
  }

  function handleImport(): void {
    if (!importCandidate) return

    props.onImport(importCandidate)
    setImportCandidate(null)
    toast.success(t('Playground backup imported'))
  }

  return (
    <>
      <div className='grid grid-cols-2 gap-2'>
        <Button
          disabled={props.disabled}
          onClick={handleExport}
          type='button'
          variant='outline'
        >
          <DownloadIcon aria-hidden='true' />
          {t('Export backup')}
        </Button>
        <Button
          disabled={props.disabled}
          onClick={() => fileInputRef.current?.click()}
          type='button'
          variant='outline'
        >
          <UploadIcon aria-hidden='true' />
          {t('Import backup')}
        </Button>
      </div>

      <input
        accept='application/json,.json'
        aria-label={t('Import playground backup')}
        className='sr-only'
        onChange={handleFileChange}
        ref={fileInputRef}
        type='file'
      />

      <ConfirmDialog
        desc={t(
          'Importing this backup will replace the current chat settings and conversation history.'
        )}
        confirmText={t('Import')}
        handleConfirm={handleImport}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setImportCandidate(null)
        }}
        open={importCandidate !== null}
        title={t('Import playground backup?')}
      />
    </>
  )
}
