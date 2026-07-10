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

import { Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { OAuthAppSecret } from '../types'

interface OAuthAppSecretDialogProps {
  secret: OAuthAppSecret | null
  onOpenChange: (open: boolean) => void
}

async function copyToClipboard(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value)
    return true
  } catch {
    return false
  }
}

export function OAuthAppSecretDialog(props: OAuthAppSecretDialogProps) {
  const { t } = useTranslation()
  const open = props.secret !== null
  const secret = props.secret?.client_secret ?? ''
  const clientId = props.secret?.client_id

  const handleCopy = async (value: string) => {
    const ok = await copyToClipboard(value)
    toast[ok ? 'success' : 'error'](ok ? t('Copied') : t('Copy failed'))
  }

  return (
    <Dialog open={open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-xl'>
        <DialogHeader>
          <DialogTitle>{t('OAuth client secret')}</DialogTitle>
          <DialogDescription>
            {t('Copy the client secret now. It will not be shown again.')}
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-3'>
          {clientId ? (
            <div className='space-y-1'>
              <div className='text-sm font-medium'>{t('Client ID')}</div>
              <div className='flex items-center gap-2 rounded-lg border bg-muted/30 px-3 py-2 font-mono text-xs'>
                <span className='min-w-0 flex-1 truncate'>{clientId}</span>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => handleCopy(clientId)}
                >
                  <Copy className='size-4' />
                  <span className='sr-only'>{t('Copy')}</span>
                </Button>
              </div>
            </div>
          ) : null}
          <div className='space-y-1'>
            <div className='text-sm font-medium'>{t('Client Secret')}</div>
            <div className='flex items-center gap-2 rounded-lg border bg-muted/30 px-3 py-2 font-mono text-xs'>
              <span className='min-w-0 flex-1 break-all'>{secret}</span>
              <Button
                type='button'
                variant='ghost'
                size='icon-sm'
                onClick={() => handleCopy(secret)}
              >
                <Copy className='size-4' />
                <span className='sr-only'>{t('Copy')}</span>
              </Button>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button onClick={() => props.onOpenChange(false)}>
            {t('I have saved it')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
