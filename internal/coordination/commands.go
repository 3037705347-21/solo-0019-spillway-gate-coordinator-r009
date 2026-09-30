package coordination

import (
	"context"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func (s *Service) RequestCommand(
	ctx context.Context,
	request contracts.RequestCommandRequest,
) (domain.GateCommand, error) {
	gate, err := s.repository.Gate(ctx, request.GateID)
	if err != nil {
		return domain.GateCommand{}, err
	}

	commandID, err := s.newID("cmd")
	if err != nil {
		return domain.GateCommand{}, domain.Internal("command_id_generation_failed", "command id could not be generated")
	}

	command, err := domain.NewGateCommand(
		commandID,
		gate.ID,
		request.TargetAperture,
		gate.Aperture,
		gate.MaxAperture,
		request.Reason,
		request.RequestedBy,
		s.now(),
	)
	if err != nil {
		return domain.GateCommand{}, err
	}
	if err := s.repository.CreateCommand(ctx, command); err != nil {
		return domain.GateCommand{}, err
	}
	return command, nil
}

func (s *Service) Command(ctx context.Context, id string) (domain.GateCommand, error) {
	return s.repository.Command(ctx, id)
}

func (s *Service) ReviewInterlock(
	ctx context.Context,
	commandID string,
	request contracts.ReviewInterlockRequest,
) (domain.GateCommand, domain.Gate, error) {
	decision, err := domain.NewInterlockDecision(
		request.Verdict,
		request.Verifier,
		request.Note,
		s.now(),
	)
	if err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	grant := domain.ExecutionGrant{}
	if decision.IsClear() {
		command, err := s.repository.Command(ctx, commandID)
		if err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		token, err := s.newToken()
		if err != nil {
			return domain.GateCommand{}, domain.Gate{}, domain.Internal(
				"execution_token_generation_failed",
				"execution token could not be generated",
			)
		}
		grant, err = s.newExecutionGrant(commandID, command.GateID, token)
		if err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	}

	return s.repository.ReviewCommand(ctx, commandID, decision, grant, s.now())
}

func (s *Service) newExecutionGrant(
	commandID string,
	gateID string,
	token string,
) (domain.ExecutionGrant, error) {
	grantID, err := s.newID("grant")
	if err != nil {
		return domain.ExecutionGrant{}, domain.Internal(
			"execution_grant_generation_failed",
			"execution grant could not be generated",
		)
	}
	return domain.NewExecutionGrant(grantID, commandID, gateID, token, s.now())
}

func (s *Service) ExecuteCommand(
	ctx context.Context,
	commandID string,
	request contracts.ExecuteCommandRequest,
) (domain.GateCommand, domain.Gate, error) {
	return s.repository.ExecuteCommand(
		ctx,
		commandID,
		request.Token,
		request.Operator,
		request.SimulateFault,
		s.now(),
	)
}
