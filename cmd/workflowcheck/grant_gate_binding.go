package main

import (
	"fmt"
	"net/http"
)

func runGrantGateBinding(client *apiClient) error {
	if _, err := fixtureGate(client, "gate-alpha", "甲泄洪闸", 5, 0.5); err != nil {
		return err
	}
	if _, err := fixtureGate(client, "gate-bravo", "乙泄洪闸", 5, 0.5); err != nil {
		return err
	}

	commandAlpha, err := fixtureCommand(client, "gate-alpha", 1.5, "甲闸门开度调整", "dispatcher-yang")
	if err != nil {
		return err
	}
	alphaID, err := stringField(commandAlpha, "id")
	if err != nil {
		return err
	}
	commandBravo, err := fixtureCommand(client, "gate-bravo", 2.5, "乙闸门开度调整", "dispatcher-yang")
	if err != nil {
		return err
	}
	bravoID, err := stringField(commandBravo, "id")
	if err != nil {
		return err
	}

	// 同一安全复核员先后为两座不同闸门放行，各自取得执行凭据。
	approvedAlpha, err := fixtureClearReview(client, alphaID, "safety-wu")
	if err != nil {
		return err
	}
	alphaToken, err := stringField(approvedAlpha, "execution_token")
	if err != nil {
		return err
	}
	approvedBravo, err := fixtureClearReview(client, bravoID, "safety-wu")
	if err != nil {
		return err
	}
	bravoToken, err := stringField(approvedBravo, "execution_token")
	if err != nil {
		return err
	}
	if alphaToken == bravoToken {
		return fmt.Errorf("expected distinct execution tokens for the two commands")
	}

	// 拿甲闸门命令的凭据提交乙闸门命令：必须被拒绝。
	crossUse, err := client.post("/commands/"+bravoID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          alphaToken,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(crossUse, http.StatusConflict, "reject credential cross-use across gates"); err != nil {
		return err
	}
	if code, err := stringField(crossUse.Body, "code"); err != nil || code != "invalid_execution_grant" {
		return fmt.Errorf("cross-use rejection: expected code invalid_execution_grant, got %#v", crossUse.Body)
	}

	// 失败尝试不能改变任何状态：乙闸门仍停留在 reserved，乙命令仍是 approved。
	if err := expectGate(client, "gate-bravo", "reserved", 0.5, bravoID); err != nil {
		return fmt.Errorf("gate bravo changed after rejected attempt: %w", err)
	}
	if err := expectCommandStatus(client, bravoID, "approved"); err != nil {
		return fmt.Errorf("command bravo changed after rejected attempt: %w", err)
	}

	// 甲闸门的凭据在这次失败尝试后必须仍然有效并可首次执行。
	executeAlpha, err := client.post("/commands/"+alphaID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          alphaToken,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(executeAlpha, http.StatusOK, "execute gate alpha with its own credential"); err != nil {
		return err
	}
	if err := expectGate(client, "gate-alpha", "idle", 1.5, ""); err != nil {
		return err
	}

	// 甲闸门正常执行不能影响乙闸门尚未使用的凭据。
	if err := expectGate(client, "gate-bravo", "reserved", 0.5, bravoID); err != nil {
		return fmt.Errorf("gate bravo changed after alpha execution: %w", err)
	}
	executeBravo, err := client.post("/commands/"+bravoID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          bravoToken,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(executeBravo, http.StatusOK, "execute gate bravo after gate alpha completed"); err != nil {
		return err
	}
	if err := expectGate(client, "gate-bravo", "idle", 2.5, ""); err != nil {
		return err
	}

	// 执行结束后凭据不可复用：甲的凭据不能再用于乙命令。
	reusedOnBravo, err := client.post("/commands/"+bravoID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          alphaToken,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(reusedOnBravo, http.StatusConflict, "reject consumed credential reuse"); err != nil {
		return err
	}

	fmt.Println("grant-gate-binding: cross-gate credential use rejected; valid credentials survived and completed independently")
	return nil
}

func expectGate(client *apiClient, id, status string, aperture float64, activeCommandID string) error {
	result, err := client.get("/gates/" + id)
	if err != nil {
		return err
	}
	if err := requireStatus(result, http.StatusOK, "read gate "+id); err != nil {
		return err
	}
	gate, err := objectField(result.Body, "gate")
	if err != nil {
		return err
	}
	gotStatus, err := stringField(gate, "status")
	if err != nil {
		return err
	}
	gotAperture, err := floatField(gate, "aperture")
	if err != nil {
		return err
	}
	gotActiveCommand, _ := stringField(gate, "active_command_id")
	if gotStatus != status || gotAperture != aperture || gotActiveCommand != activeCommandID {
		return fmt.Errorf(
			"gate %s mismatch: status=%q aperture=%v active_command_id=%q; want status=%q aperture=%v active_command_id=%q",
			id, gotStatus, gotAperture, gotActiveCommand, status, aperture, activeCommandID,
		)
	}
	return nil
}

func expectCommandStatus(client *apiClient, id, status string) error {
	result, err := client.get("/commands/" + id)
	if err != nil {
		return err
	}
	if err := requireStatus(result, http.StatusOK, "read command "+id); err != nil {
		return err
	}
	command, err := objectField(result.Body, "command")
	if err != nil {
		return err
	}
	gotStatus, err := stringField(command, "status")
	if err != nil {
		return err
	}
	if gotStatus != status {
		return fmt.Errorf("command %s status=%q, want %q", id, gotStatus, status)
	}
	return nil
}
