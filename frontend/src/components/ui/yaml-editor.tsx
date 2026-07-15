'use client';

import { lazy, Suspense, useRef, useCallback } from 'react';
import { CheckCircle2, Copy, Download, Save, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { toastError, toastSuccess } from '@/lib/toast';
import { ActionButton } from '@/components/ui/action-button';

const MonacoEditor = lazy(() => import('@monaco-editor/react'));

function EditorLoading() {
  return (
    <div className="flex items-center justify-center h-full bg-[#1e1e1e]">
      <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
    </div>
  );
}

interface YamlEditorProps {
  value: string;
  onChange?: (value: string) => void;
  readOnly?: boolean;
  onSave?: (value: string) => void;
  onDryRun?: (value: string) => void;
  saving?: boolean;
  dryRunning?: boolean;
  saveBlocked?: boolean;
  height?: string;
  className?: string;
}

export function YamlEditor({
  value,
  onChange,
  readOnly = false,
  onSave,
  onDryRun,
  saving,
  dryRunning,
  saveBlocked,
  height = '100%',
  className,
}: YamlEditorProps) {
  const editorRef = useRef<unknown>(null);

  const handleEditorDidMount = useCallback((editor: unknown) => {
    editorRef.current = editor;
  }, []);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(value);
      toastSuccess('Copied to clipboard');
    } catch {
      toastError('Failed to copy');
    }
  }, [value]);

  const handleDownload = useCallback(() => {
    const blob = new Blob([value], { type: 'text/yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'resource.yaml';
    a.click();
    URL.revokeObjectURL(url);
  }, [value]);

  const handleSave = useCallback(() => {
    if (onSave) onSave(value);
  }, [onSave, value]);

  const handleDryRun = useCallback(() => {
    if (onDryRun) onDryRun(value);
  }, [onDryRun, value]);

  return (
    <div className={cn('flex flex-col', className)}>
      {/* Toolbar */}
      <div className="flex items-center justify-between px-3 py-1.5 bg-[#252526] border-b border-[#3c3c3c]">
        <span className="text-xs text-[#cccccc]/60">YAML</span>
        <div className="flex items-center gap-1">
          <button onClick={handleCopy} className="p-1.5 rounded hover:bg-[#3c3c3c] text-[#cccccc]/80 hover:text-[#cccccc] transition-colors" title="Copy">
            <Copy className="h-3.5 w-3.5" />
          </button>
          <button onClick={handleDownload} className="p-1.5 rounded hover:bg-[#3c3c3c] text-[#cccccc]/80 hover:text-[#cccccc] transition-colors" title="Download">
            <Download className="h-3.5 w-3.5" />
          </button>
          {!readOnly && onDryRun && (
            <ActionButton
              onClick={handleDryRun}
              disabled={dryRunning || saving}
              icon={<CheckCircle2 className="h-3 w-3" />}
              loading={dryRunning}
              loadingLabel="Dry run"
              size="sm"
              className="ml-1 h-7 border-0 bg-[#3c3c3c] px-2.5 py-1 text-[#cccccc] hover:bg-[#4a4a4a]"
              title="Dry run"
            >
              Dry run
            </ActionButton>
          )}
          {!readOnly && onSave && (
            <ActionButton
              onClick={handleSave}
              disabled={saving || dryRunning}
              disabledReason={saveBlocked ? 'Run dry run and review the diff before saving' : undefined}
              icon={<Save className="h-3 w-3" />}
              intent="primary"
              loading={saving}
              loadingLabel="Save"
              size="sm"
              title="Save"
              className="ml-1 h-7 bg-blue-600 px-2.5 py-1 text-white hover:bg-blue-700"
            >
              Save
            </ActionButton>
          )}
        </div>
      </div>

      {/* Editor */}
      <div className="flex-1 min-h-0" style={{ height }}>
        <Suspense fallback={<EditorLoading />}>
          <MonacoEditor
            language="yaml"
            theme="vs-dark"
            value={value}
            onChange={(v) => onChange?.(v || '')}
            onMount={handleEditorDidMount}
            options={{
              readOnly,
              minimap: { enabled: false },
              fontSize: 13,
              lineNumbers: 'on',
              scrollBeyondLastLine: false,
              wordWrap: 'on',
              tabSize: 2,
              automaticLayout: true,
              renderLineHighlight: 'line',
              folding: true,
              padding: { top: 8 },
            }}
          />
        </Suspense>
      </div>
    </div>
  );
}
