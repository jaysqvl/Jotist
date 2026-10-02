import { useMemo, useRef, useState, type ReactNode } from 'react';
import { Check, ChevronsUpDown, Clock3, Pin } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
	Command,
	CommandEmpty,
	CommandInput,
	CommandItem,
	CommandList
} from '@/components/ui/command';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import { useIsMobile } from '@/hooks/use-mobile';
import { Dialog, DialogContent, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import type { ExecutionRun } from '../../hooks/useAudioDetail';
import {
	filterRunChoices,
	runChoicePresentation,
	runElapsedLabel,
	runOutcome,
	runOutcomeLabel,
	runRecoveryBehavior,
	runStatusLabel,
	type RunDeviceFilter,
	type RunStatusFilter
} from '../../hooks/runChoices';

export function RunStatusBadge({ status, run }: { status?: string; run?: ExecutionRun }) {
	const normalized = run ? runOutcome(run) : status?.toLowerCase();
	return (
		<span
			className={cn(
				'inline-flex shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-semibold',
				normalized === 'recovered'
					? 'bg-violet-500/10 text-violet-700 dark:text-violet-300'
					: normalized === 'completed'
						? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
						: normalized === 'failed'
							? 'bg-red-500/10 text-red-700 dark:text-red-300'
							: ['processing', 'running', 'pending', 'queued', 'waiting_for_resource'].includes(
										normalized || ''
								  )
								? 'bg-amber-500/10 text-amber-700 dark:text-amber-300'
								: 'bg-[var(--bg-main)] text-[var(--text-secondary)]'
			)}
		>
			<span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-current" />
			{run ? runOutcomeLabel(run) : runStatusLabel(status)}
		</span>
	);
}

export function RunPicker({
	runs,
	activeRunId,
	pinnedRunId,
	value,
	onValueChange,
	label,
	compact = false
}: {
	runs: ExecutionRun[];
	activeRunId?: string;
	pinnedRunId?: string;
	value?: string;
	onValueChange: (runId: string) => void;
	label: string;
	compact?: boolean;
}) {
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState('');
	const [status, setStatus] = useState<RunStatusFilter>('all');
	const [device, setDevice] = useState<RunDeviceFilter>('all');
	const selected = runs.find((run) => run.id === value);
	const summary = selected ? runChoicePresentation(selected) : null;
	const filtered = useMemo(
		() => filterRunChoices(runs, query, status, device),
		[runs, query, status, device]
	);
	const reset = () => {
		setQuery('');
		setStatus('all');
		setDevice('all');
	};
	const mobile = useIsMobile();
	const sheetRef = useRef<HTMLDivElement>(null);
	const changeOpen = (next: boolean) => {
		setOpen(next);
		if (next) reset();
	};
	const trigger = (
		<Button
			variant="outline"
			aria-label={label}
			aria-expanded={open}
			aria-haspopup="dialog"
			className={cn(
				'h-auto min-h-14 min-w-0 justify-between gap-3 border-[var(--border-subtle)] bg-[var(--bg-card)] px-3 py-2 text-left text-[var(--text-primary)]',
				compact ? 'mt-1 w-full' : 'w-full sm:w-[320px]'
			)}
		>
			<span className="min-w-0 flex-1">
				{selected && summary ? (
					<>
						<span className="flex flex-wrap items-center gap-2 text-xs">
							<span className="font-semibold">Run {selected.run_number}</span>
							<RunStatusBadge run={selected} />
							{selected.id === pinnedRunId && (
								<Pin aria-label="Pinned transcript" className="h-3 w-3 text-[var(--brand-solid)]" />
							)}
						</span>
						<span className="mt-1 block truncate text-xs font-normal text-[var(--text-secondary)]">
							{summary.model} · {summary.device}
						</span>
					</>
				) : (
					'Choose a run'
				)}
			</span>
			<ChevronsUpDown className="h-4 w-4 shrink-0 opacity-60" aria-hidden="true" />
		</Button>
	);
	const title = (
		<span className="flex items-center justify-between gap-2">
			<span>Choose a run</span>
			<span className="text-xs font-normal text-[var(--text-tertiary)]">{runs.length} runs</span>
		</span>
	);
	const picker = (
		<Command
			shouldFilter={false}
			label={`${label} options`}
			className="flex min-h-0 flex-1 flex-col bg-transparent text-[var(--text-primary)]"
		>
			<div className={cn('shrink-0 px-4 pt-4 text-sm font-semibold', mobile && 'pr-12')}>
				{mobile ? <DialogTitle className="text-base">{title}</DialogTitle> : <h3>{title}</h3>}
			</div>
			<CommandInput
				aria-label="Search runs"
				placeholder="Search runs, models or profiles…"
				value={query}
				onValueChange={setQuery}
				className="text-base sm:text-sm"
			/>
			<div className="shrink-0 border-b border-[var(--border-subtle)] p-3">
				<div className="grid grid-cols-2 gap-3 sm:hidden">
					<label className="min-w-0 text-[11px] text-[var(--text-secondary)]">
						Status
						<select
							aria-label="Filter by run status"
							value={status}
							onChange={(e) => setStatus(e.target.value as RunStatusFilter)}
							onKeyDown={(e) => e.stopPropagation()}
							className="mt-1 block h-10 w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-main)] px-2 text-sm text-[var(--text-primary)]"
						>
							<option value="all">All runs</option>
							<option value="completed">Completed</option>
							<option value="recovered">With recovery</option>
							<option value="failed">Failed</option>
							<option value="in_progress">In progress</option>
						</select>
					</label>
					<label className="min-w-0 text-[11px] text-[var(--text-secondary)]">
						Device
						<select
							aria-label="Filter by device"
							value={device}
							onChange={(e) => setDevice(e.target.value as RunDeviceFilter)}
							onKeyDown={(e) => e.stopPropagation()}
							className="mt-1 block h-10 w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-main)] px-2 text-sm text-[var(--text-primary)]"
						>
							<option value="all">Any device</option>
							<option value="cuda">GPU</option>
							<option value="cpu">CPU</option>
						</select>
					</label>
				</div>
				<div className="hidden flex-wrap items-center justify-between gap-2 sm:flex">
					<div role="group" aria-label="Filter by run status" className="flex flex-wrap gap-1">
						{(
							[
								['all', 'All'],
								['completed', 'Completed'],
								['recovered', 'With recovery'],
								['failed', 'Failed'],
								['in_progress', 'In progress']
							] as const
						).map(([key, text]) => (
							<FilterButton key={key} selected={status === key} onClick={() => setStatus(key)}>
								{text}
							</FilterButton>
						))}
					</div>
					<div role="group" aria-label="Filter by device" className="flex gap-1">
						{(
							[
								['all', 'Any device'],
								['cuda', 'GPU'],
								['cpu', 'CPU']
							] as const
						).map(([key, text]) => (
							<FilterButton key={key} selected={device === key} onClick={() => setDevice(key)}>
								{text}
							</FilterButton>
						))}
					</div>
				</div>
			</div>
			<CommandList className="max-h-none min-h-0 flex-1 overscroll-contain p-2">
				<CommandEmpty className="px-4 py-8 text-[var(--text-secondary)]">
					No runs match these filters.
					<button
						type="button"
						onClick={reset}
						className="mt-2 block w-full text-[var(--brand-solid)] underline"
					>
						Show all runs
					</button>
				</CommandEmpty>
				{filtered.map((run) => {
					const row = runChoicePresentation(run),
						recovery = runRecoveryBehavior(run);
					return (
						<CommandItem
							key={run.id}
							value={run.id}
							onSelect={() => {
								onValueChange(run.id);
								setOpen(false);
							}}
							className="my-1 cursor-pointer items-start gap-2 rounded-xl border border-transparent px-3 py-3 data-[selected=true]:border-[var(--border-subtle)] data-[selected=true]:bg-[var(--bg-main)]"
						>
							<span className="mt-1 w-4 shrink-0">
								{run.id === value && (
									<Check className="h-4 w-4 text-[var(--brand-solid)]" aria-label="Selected run" />
								)}
							</span>
							<span className="min-w-0 flex-1">
								<span className="flex flex-wrap items-center gap-x-2 gap-y-1">
									<span className="text-xs font-semibold">Run {run.run_number}</span>
									<RunStatusBadge run={run} />
									<span className="ml-auto inline-flex items-center gap-1 text-xs text-[var(--text-secondary)] tabular-nums">
										<Clock3 className="h-3 w-3" />
										{runElapsedLabel(run)}
									</span>
								</span>
								<span className="mt-2 block text-sm font-medium break-words">{row.model}</span>
								<span className="mt-1 block text-xs break-words text-[var(--text-secondary)]">
									{row.speakers}
									{row.speakerRuntime && ` · ${row.speakerRuntime}`}
								</span>
								<span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-[var(--text-tertiary)]">
									<span>
										{row.device} · {row.precision}
									</span>
									{run.id === pinnedRunId ? (
										<span className="inline-flex items-center gap-1 text-[var(--brand-solid)]">
											<Pin className="h-3 w-3" />
											Pinned
										</span>
									) : (
										run.id === activeRunId && <span>Current transcript</span>
									)}
								</span>
								{run.profile_name && (
									<span
										className="mt-1 block truncate text-[11px] text-[var(--text-tertiary)]"
										title={run.profile_name}
									>
										{run.profile_name}
									</span>
								)}
								{recovery.details && (
									<span className="mt-1 block text-[11px] break-words text-violet-700 dark:text-violet-300">
										{recovery.details}
										{['failed', 'cancelled', 'interrupted'].includes(run.status || '') &&
											' · did not complete'}
									</span>
								)}
							</span>
						</CommandItem>
					);
				})}
			</CommandList>
			<div
				className="shrink-0 border-t border-[var(--border-subtle)] px-4 py-3 text-[11px] text-[var(--text-tertiary)]"
				style={{ paddingBottom: mobile ? 'max(12px, env(safe-area-inset-bottom))' : undefined }}
			>
				{filtered.length} of {runs.length} runs · “Requested” = runtime not recorded.
			</div>
		</Command>
	);
	return mobile ? (
		<Dialog open={open} onOpenChange={changeOpen}>
			<DialogTrigger asChild>{trigger}</DialogTrigger>
			<DialogContent
				ref={sheetRef}
				tabIndex={-1}
				aria-describedby={undefined}
				className="inset-x-0 top-auto bottom-0 flex h-[min(680px,calc(100dvh-1rem))] max-h-[calc(100dvh-1rem)] w-full max-w-none translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-t-2xl rounded-b-none border border-[var(--border-subtle)] bg-[var(--bg-card)] p-0"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					sheetRef.current?.focus();
				}}
			>
				{picker}
			</DialogContent>
		</Dialog>
	) : (
		<Popover open={open} onOpenChange={changeOpen} modal>
			<PopoverTrigger asChild>{trigger}</PopoverTrigger>
			<PopoverContent
				align="start"
				collisionPadding={16}
				aria-label={`${label} picker`}
				className="flex h-[min(600px,var(--radix-popover-content-available-height))] max-h-[var(--radix-popover-content-available-height)] w-[min(640px,calc(100vw-2rem))] flex-col overflow-hidden rounded-xl border-[var(--border-subtle)] bg-[var(--bg-card)] p-0 shadow-xl"
			>
				{picker}
			</PopoverContent>
		</Popover>
	);
}

function FilterButton({
	selected,
	onClick,
	children
}: {
	selected: boolean;
	onClick: () => void;
	children: ReactNode;
}) {
	return (
		<button
			type="button"
			aria-pressed={selected}
			onClick={onClick}
			className={cn(
				'min-h-9 rounded-md px-2 py-1.5 text-[11px] font-medium transition-colors',
				selected
					? 'bg-[var(--brand-light)] text-[var(--brand-solid)]'
					: 'text-[var(--text-secondary)] hover:bg-[var(--bg-main)]'
			)}
		>
			{children}
		</button>
	);
}
