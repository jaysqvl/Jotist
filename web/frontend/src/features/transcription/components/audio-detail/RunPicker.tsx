import { useMemo, useState, type ReactNode } from 'react';
import { Check, ChevronsUpDown, Clock3, Cpu, Pin } from 'lucide-react';
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
	return (
		<Popover
			open={open}
			onOpenChange={(next) => {
				setOpen(next);
				if (next) reset();
			}}
		>
			<PopoverTrigger asChild>
				<Button
					variant="outline"
					aria-label={label}
					aria-expanded={open}
					aria-haspopup="dialog"
					className={cn(
						'h-auto min-h-14 justify-between gap-3 border-[var(--border-subtle)] bg-[var(--bg-card)] px-3 py-2 text-left text-[var(--text-primary)]',
						compact ? 'mt-1 w-full' : 'w-full sm:w-[360px]'
					)}
				>
					<span className="min-w-0 flex-1">
						{selected && summary ? (
							<>
								<span className="flex flex-wrap items-center gap-2 text-xs">
									<span className="font-semibold">Run {selected.run_number}</span>
									<RunStatusBadge run={selected} />
									<span className="text-[var(--text-secondary)]">{summary.device}</span>
									{selected.id === pinnedRunId && (
										<Pin
											aria-label="Pinned transcript"
											className="h-3 w-3 text-[var(--brand-solid)]"
										/>
									)}
								</span>
								<span className="mt-1 block truncate text-xs font-normal text-[var(--text-secondary)]">
									{summary.model}
								</span>
							</>
						) : (
							'Choose a run'
						)}
					</span>
					<ChevronsUpDown className="h-4 w-4 shrink-0 opacity-60" aria-hidden="true" />
				</Button>
			</PopoverTrigger>
			<PopoverContent
				align="start"
				aria-label={`${label} picker`}
				className="w-[min(640px,calc(100vw-2rem))] overflow-hidden rounded-xl border-[var(--border-subtle)] bg-[var(--bg-card)] p-0 shadow-xl"
			>
				<Command
					shouldFilter={false}
					label={`${label} options`}
					className="bg-transparent text-[var(--text-primary)]"
				>
					<div className="flex items-center justify-between gap-2 px-4 pt-3 text-sm font-semibold">
						<span>Choose a run</span>
						<span className="text-xs font-normal text-[var(--text-tertiary)]">
							{runs.length} runs
						</span>
					</div>
					<CommandInput
						aria-label="Search runs"
						placeholder="Search model, profile, speakers, or run number…"
						value={query}
						onValueChange={setQuery}
						className="text-[var(--text-primary)]"
					/>
					<div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] p-2">
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
					<CommandList className="max-h-[min(440px,55vh)] p-2">
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
							const row = runChoicePresentation(run);
							return (
								<CommandItem
									key={run.id}
									value={run.id}
									onSelect={() => {
										onValueChange(run.id);
										setOpen(false);
									}}
									className="my-1 cursor-pointer items-start gap-3 rounded-lg border border-transparent px-3 py-3 data-[selected=true]:border-[var(--border-subtle)] data-[selected=true]:bg-[var(--bg-main)]"
								>
									<span className="mt-1 w-4 shrink-0">
										{run.id === value && (
											<Check
												className="h-4 w-4 text-[var(--brand-solid)]"
												aria-label="Selected run"
											/>
										)}
									</span>
									<span className="min-w-0 flex-1">
										<span className="flex flex-wrap items-center gap-2">
											<span className="text-xs font-semibold">Run {run.run_number}</span>
											<RunStatusBadge run={run} />
											{run.id === pinnedRunId ? (
												<span className="inline-flex items-center gap-1 text-[10px] text-[var(--brand-solid)]">
													<Pin className="h-3 w-3" />
													Pinned
												</span>
											) : (
												run.id === activeRunId && (
													<span className="text-[10px] text-[var(--text-tertiary)]">
														Current transcript
													</span>
												)
											)}
											<span className="ml-auto inline-flex items-center gap-1 text-xs text-[var(--text-secondary)] tabular-nums">
												<Clock3 className="h-3 w-3" />
												{runElapsedLabel(run)}
											</span>
										</span>
										{runRecoveryBehavior(run).details && (
											<span className="mt-1 block text-[11px] text-violet-700 dark:text-violet-300">
												{runRecoveryBehavior(run).details}
												{['failed', 'cancelled', 'interrupted'].includes(run.status || '') &&
													' · did not complete'}
											</span>
										)}
										{run.profile_name && (
											<span className="mt-1 block text-xs font-medium break-words text-[var(--text-primary)]">
												{run.profile_name}
											</span>
										)}
										<span className="mt-1 block text-xs text-[var(--text-secondary)]">
											{row.model}
										</span>
										<span className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[var(--text-secondary)]">
											<span className="inline-flex items-center gap-1">
												<Cpu className="h-3 w-3" />
												{row.device}
											</span>
											<span>{row.precision}</span>
											<span>
												{row.speakers}
												{row.speakerRuntime && ` · ${row.speakerRuntime}`}
											</span>
										</span>
									</span>
								</CommandItem>
							);
						})}
					</CommandList>
					<div className="border-t border-[var(--border-subtle)] px-4 py-2 text-[10px] text-[var(--text-tertiary)]">
						{filtered.length} of {runs.length} runs · Device filters match transcription or
						speakers. “Requested” means the runtime was not recorded.
					</div>
				</Command>
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
				'rounded-md px-2 py-1.5 text-[11px] font-medium transition-colors',
				selected
					? 'bg-[var(--brand-light)] text-[var(--brand-solid)]'
					: 'text-[var(--text-secondary)] hover:bg-[var(--bg-main)]'
			)}
		>
			{children}
		</button>
	);
}
