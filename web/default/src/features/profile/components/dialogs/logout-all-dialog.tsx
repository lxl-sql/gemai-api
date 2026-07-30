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
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { useAuthStore } from '@/stores/auth-store'

import { logoutAllSessions } from '../../api'

interface LogoutAllDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function LogoutAllDialog({
  open,
  onOpenChange,
}: LogoutAllDialogProps) {
  const { t } = useTranslation()
  const { auth } = useAuthStore()

  const handleConfirm = async () => {
    const result = await logoutAllSessions()
    if (!result.success) {
      throw new Error(result.message || t('Failed to revoke sessions'))
    }
    auth.reset()
    localStorage.removeItem('uid')
    toast.success(t('All sessions and delegated access have been revoked'))
    window.location.reload()
  }

  return (
    <ConfirmDialog
      destructive
      open={open}
      onOpenChange={onOpenChange}
      title={t('Sign out all devices')}
      desc={t(
        'This signs out every browser session and revokes account access tokens and delegated OAuth grants.'
      )}
      confirmText={t('Revoke all access')}
      handleConfirm={handleConfirm}
      className='sm:max-w-md'
    />
  )
}
