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
import { Check, Copy, KeyRound, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Item,
  ItemContent,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from '@/components/ui/item'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import { useApiKeys } from './api-keys-provider'

export function ApiKeySecretDialog() {
  const { t } = useTranslation()
  const { open, setOpen, oneTimeSecrets, setOneTimeSecrets } = useApiKeys()
  const isOpen = open === 'secret'
  const { copiedText, copyToClipboard } = useCopyToClipboard()
  const showIndex = oneTimeSecrets.length > 1

  const close = () => {
    setOneTimeSecrets([])
    setOpen(null)
  }

  const copyAllValue = oneTimeSecrets
    .map((item) => `${item.name}\t${item.key}`)
    .join('\n')
  const copiedAll = copiedText === copyAllValue && copyAllValue !== ''

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) close()
      }}
      title={
        <span className='flex items-center gap-2'>
          <KeyRound className='size-5' />
          {t('Save your API key now')}
        </span>
      }
      description={t(
        'For security, the complete key will not be shown again after this dialog is closed.'
      )}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => copyToClipboard(copyAllValue)}
          >
            {copiedAll ? (
              <Check data-icon='inline-start' className='text-success' />
            ) : (
              <Copy data-icon='inline-start' />
            )}
            {t('Copy all')}
          </Button>
          <Button onClick={close}>{t('I have saved the key')}</Button>
        </>
      }
    >
      <div className='space-y-4'>
        <Alert>
          <TriangleAlert className='text-amber-500' />
          <AlertTitle>{t('One-time display')}</AlertTitle>
          <AlertDescription>
            {t(
              'If this key is lost, rotate it to issue a replacement. The old key will stop working immediately.'
            )}
          </AlertDescription>
        </Alert>
        <ItemGroup>
          {oneTimeSecrets.map((item, index) => (
            <Item
              key={item.id}
              variant='outline'
              className='bg-muted/30 flex-col flex-nowrap items-stretch gap-2.5 px-2.5 sm:px-3'
            >
              <div className='flex w-full min-w-0 items-center justify-between gap-2'>
                <ItemContent className='min-w-0 flex-1 flex-row items-center gap-1.5'>
                  <ItemMedia variant='icon' className='text-muted-foreground'>
                    <KeyRound />
                  </ItemMedia>
                  <ItemTitle
                    className='w-auto min-w-0 flex-1'
                    title={item.name}
                  >
                    {item.name || t('API Key')}
                  </ItemTitle>
                </ItemContent>
                {showIndex ? (
                  <Badge variant='outline' className='shrink-0 font-mono'>
                    #{index + 1}
                  </Badge>
                ) : null}
              </div>
              <div className='bg-background flex w-full min-w-0 items-start gap-2 rounded-md border px-2.5 py-2'>
                <code className='min-w-0 flex-1 font-mono text-xs leading-5 break-all select-all'>
                  {item.key}
                </code>
                <CopyButton
                  value={item.key}
                  size='icon'
                  tooltip={t('Copy API key')}
                  className='-my-0.5 size-7 shrink-0'
                />
              </div>
            </Item>
          ))}
        </ItemGroup>
      </div>
    </Dialog>
  )
}
