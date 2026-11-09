import { ChangeDetectionStrategy, Component } from '@angular/core';
import { WorkspaceShellComponent } from './presentation/layout/workspace-shell/workspace-shell.component';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [WorkspaceShellComponent],
  templateUrl: './app.html',
  styleUrl: './app.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class App {}
