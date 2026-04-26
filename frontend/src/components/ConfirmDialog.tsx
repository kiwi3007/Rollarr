import { AlertTriangle } from 'lucide-react';

interface ConfirmDialogProps {
  title: string;
  message: string;
  confirmLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({
  title,
  message,
  confirmLabel = 'Confirm',
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      style={{ background: 'rgba(0,0,0,0.7)', backdropFilter: 'blur(4px)' }}
      onClick={onCancel}
    >
      <div
        className="w-full max-w-sm rounded-xl p-6 space-y-4 fade-up"
        style={{
          background: 'var(--bg-elevated)',
          border: '1px solid rgba(255,255,255,0.1)',
          boxShadow: '0 24px 64px rgba(0,0,0,0.6)',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start gap-3">
          <div className="p-2 rounded-lg" style={{ background: 'rgba(239,68,68,0.15)' }}>
            <AlertTriangle size={18} style={{ color: 'var(--red)' }} />
          </div>
          <div>
            <h3 className="font-bold text-white">{title}</h3>
            <p className="text-sm mt-1" style={{ color: 'rgba(255,255,255,0.5)' }}>{message}</p>
          </div>
        </div>
        <div className="flex gap-3 justify-end pt-2">
          <button
            onClick={onCancel}
            className="px-4 py-2 rounded-lg text-sm font-medium transition-colors"
            style={{
              background: 'rgba(255,255,255,0.06)',
              color: 'rgba(255,255,255,0.7)',
            }}
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            className="px-4 py-2 rounded-lg text-sm font-semibold transition-all"
            style={{
              background: 'rgba(239,68,68,0.2)',
              border: '1px solid rgba(239,68,68,0.4)',
              color: '#fca5a5',
            }}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
