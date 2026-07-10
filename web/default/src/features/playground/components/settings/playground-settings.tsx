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
import { Settings2Icon } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { PromptInputButton } from '@/components/ai-elements/prompt-input'
import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

import type { PlaygroundBackupData } from '../../lib'
import type { Message, ParameterEnabled, PlaygroundConfig } from '../../types'
import { PlaygroundBackupControls } from './playground-backup-controls'

type SliderParameterKey =
  | 'temperature'
  | 'top_p'
  | 'frequency_penalty'
  | 'presence_penalty'

type PlaygroundSettingsProps = {
  config: PlaygroundConfig
  disabled?: boolean
  onConfigChange: <K extends keyof PlaygroundConfig>(
    key: K,
    value: PlaygroundConfig[K]
  ) => void
  onParameterEnabledChange: (
    key: keyof ParameterEnabled,
    value: boolean
  ) => void
  onImportBackup: (backup: PlaygroundBackupData) => void
  onReset: () => void
  parameterEnabled: ParameterEnabled
  messages: Message[]
}

type ParameterSliderProps = {
  description: string
  enabled: boolean
  label: string
  max: number
  min: number
  onEnabledChange: (enabled: boolean) => void
  onValueChange: (value: number) => void
  step: number
  value: number
}

function ParameterSlider(props: ParameterSliderProps) {
  return (
    <div
      className={cn(
        'space-y-3 transition-opacity',
        !props.enabled && 'opacity-60'
      )}
    >
      <div className='flex items-start justify-between gap-3'>
        <div className='min-w-0 space-y-1'>
          <div className='flex items-center gap-2'>
            <Label>{props.label}</Label>
            <Badge variant='secondary' className='tabular-nums'>
              {props.value}
            </Badge>
          </div>
          <p className='text-muted-foreground text-xs leading-5'>
            {props.description}
          </p>
        </div>
        <Switch
          aria-label={props.label}
          checked={props.enabled}
          onCheckedChange={props.onEnabledChange}
        />
      </div>
      <Slider
        disabled={!props.enabled}
        max={props.max}
        min={props.min}
        onValueChange={(value) =>
          props.onValueChange(typeof value === 'number' ? value : value[0])
        }
        step={props.step}
        value={[props.value]}
      />
    </div>
  )
}

export function PlaygroundSettings(props: PlaygroundSettingsProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  const sliderParameters: Array<{
    description: string
    key: SliderParameterKey
    label: string
    max: number
    min: number
    step: number
  }> = [
    {
      description: t('Sampling temperature; lower is more deterministic'),
      key: 'temperature',
      label: t('Temperature'),
      max: 2,
      min: 0,
      step: 0.1,
    },
    {
      description: t('Nucleus sampling probability mass'),
      key: 'top_p',
      label: t('Top P'),
      max: 1,
      min: 0,
      step: 0.1,
    },
    {
      description: t('Penalises repetition of frequent tokens'),
      key: 'frequency_penalty',
      label: t('Frequency penalty'),
      max: 2,
      min: -2,
      step: 0.1,
    },
    {
      description: t('Encourages introducing new topics'),
      key: 'presence_penalty',
      label: t('Presence penalty'),
      max: 2,
      min: -2,
      step: 0.1,
    },
  ]

  function handleReset(): void {
    props.onReset()
    toast.success(t('Chat settings reset'))
  }

  return (
    <>
      <Tooltip>
        <TooltipTrigger
          render={
            <PromptInputButton
              aria-label={t('Chat settings')}
              disabled={props.disabled}
              onClick={() => setOpen(true)}
              variant='ghost'
            />
          }
        >
          <Settings2Icon size={16} />
        </TooltipTrigger>
        <TooltipContent>{t('Chat settings')}</TooltipContent>
      </Tooltip>

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent className={sideDrawerContentClassName('sm:max-w-md')}>
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>{t('Chat settings')}</SheetTitle>
            <SheetDescription>
              {t('Configure the parameters sent with each chat request.')}
            </SheetDescription>
          </SheetHeader>

          <div className={sideDrawerFormClassName()}>
            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Sampling controls')}
                description={t(
                  'Enable a parameter to include it in chat requests.'
                )}
              />
              {sliderParameters.map((parameter) => (
                <ParameterSlider
                  description={parameter.description}
                  enabled={props.parameterEnabled[parameter.key]}
                  key={parameter.key}
                  label={parameter.label}
                  max={parameter.max}
                  min={parameter.min}
                  onEnabledChange={(enabled) =>
                    props.onParameterEnabledChange(parameter.key, enabled)
                  }
                  onValueChange={(value) =>
                    props.onConfigChange(parameter.key, value)
                  }
                  step={parameter.step}
                  value={props.config[parameter.key]}
                />
              ))}
            </SideDrawerSection>

            <SideDrawerSection>
              <SideDrawerSectionHeader title={t('Response controls')} />
              <div className='space-y-2'>
                <div className='flex items-center justify-between gap-3'>
                  <Label htmlFor='playground-max-tokens'>
                    {t('Max tokens')}
                  </Label>
                  <Switch
                    aria-label={t('Max tokens')}
                    checked={props.parameterEnabled.max_tokens}
                    onCheckedChange={(enabled) =>
                      props.onParameterEnabledChange('max_tokens', enabled)
                    }
                  />
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t('Maximum number of tokens in the response')}
                </p>
                <Input
                  disabled={!props.parameterEnabled.max_tokens}
                  id='playground-max-tokens'
                  min={1}
                  onChange={(event) =>
                    props.onConfigChange(
                      'max_tokens',
                      Math.max(1, Number(event.target.value) || 1)
                    )
                  }
                  step={1}
                  type='number'
                  value={props.config.max_tokens}
                />
              </div>

              <div className='space-y-2'>
                <div className='flex items-center justify-between gap-3'>
                  <Label htmlFor='playground-seed'>{t('Seed')}</Label>
                  <Switch
                    aria-label={t('Seed')}
                    checked={props.parameterEnabled.seed}
                    onCheckedChange={(enabled) =>
                      props.onParameterEnabledChange('seed', enabled)
                    }
                  />
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t('Optional. Leave blank for a random seed.')}
                </p>
                <Input
                  disabled={!props.parameterEnabled.seed}
                  id='playground-seed'
                  onChange={(event) =>
                    props.onConfigChange(
                      'seed',
                      event.target.value === ''
                        ? null
                        : Number(event.target.value)
                    )
                  }
                  step={1}
                  type='number'
                  value={props.config.seed ?? ''}
                />
              </div>

              <div className='flex items-start justify-between gap-3'>
                <div className='space-y-1'>
                  <Label>{t('Streaming responses')}</Label>
                  <p className='text-muted-foreground text-xs'>
                    {t('Receive tokens as they are generated.')}
                  </p>
                </div>
                <Switch
                  aria-label={t('Streaming responses')}
                  checked={props.config.stream}
                  onCheckedChange={(checked) =>
                    props.onConfigChange('stream', checked)
                  }
                />
              </div>
            </SideDrawerSection>

            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Backup and restore')}
                description={t(
                  'Backups include chat settings and conversation history. Files exported by the classic playground are supported.'
                )}
              />
              <PlaygroundBackupControls
                config={props.config}
                disabled={props.disabled}
                messages={props.messages}
                onImport={props.onImportBackup}
                parameterEnabled={props.parameterEnabled}
              />
            </SideDrawerSection>
          </div>

          <SheetFooter className={sideDrawerFooterClassName('grid-cols-1')}>
            <Button onClick={handleReset} variant='outline'>
              {t('Reset chat settings')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </>
  )
}
