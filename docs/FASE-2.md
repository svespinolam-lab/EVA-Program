# EVA · Fase 2 — EVA Mobile (app del teléfono)

**Prompt 2** · rama `fase-2-mobile` (sobre `fase-4-backend`) · 15 nov 2026 · Evidencia de partida: [`docs/audit/mobile-audit.md`](audit/mobile-audit.md)

Leyenda: ✅ hecho y con prueba automática · 🟢 verificado además en navegador · 🟠 parcial · ⏭️ pendiente.

## Hecho

| Requisito | Estado | Verificación |
|---|---|---|
| **D. Salud mental: 8 secciones** (ánimo, energía/fatiga, estrés/ansiedad, interés/desgano, atención, equipo/conexión, soledad/nostalgia, motivación/moral) + **sueño con calidad** + **pregunta de seguridad personal** | ✅ 🟢 | `tools/tests/mental.test.mjs` (12 pruebas, 6 561 combinaciones). Asistente de 10 pasos en Log, sin saturar la pantalla. La respuesta de seguridad muestra apoyo en el teléfono y **nunca sale del teléfono** (probado). Hecho con Codex. |
| **B. 12 parámetros físicos con valor, unidad, fecha y hora, fuente, semáforo, tendencia de 7 días y línea base** | ✅ 🟢 | `tools/tests/measurements.test.mjs` (15 pruebas). Cada tarjeta muestra semáforo, hora, fuente (Manual / Sensor / **Demo · not a real sensor**), gráfica de 7 días y línea base. |
| Distinguir automático vs. manual | ✅ 🟢 | Fuente por registro: `manual`, `device` (sensor emparejado) o `demo` (simulado). |
| Quitar la **presión arterial inventada** al iniciar | ✅ 🟢 | En el navegador: sin medición, la PA aparece vacía. |
| **A. Perfil**: ID de tripulante, duración de misión, suplementos, contactos de emergencia; aviso de que el expediente es de ejemplo | ✅ 🟢 | Pantalla Profile. |
| **E. Ciclo menstrual opcional y privado** (apagado por defecto): inicio de periodo, flujo y síntomas con botones; fase estimada; en fase lútea la estación ajusta los límites de alerta +0,3 °C y +3 lpm. **Solo salen esos dos ajustes anónimos**, nunca fechas, fase ni síntomas. Borrado total en un toque. | ✅ 🟢 | `tools/tests/cycle.test.mjs` (9 pruebas, incluida la privacidad). Menú ≡ → Cycle tracking. |
| **C. Análisis mensuales visibles**: tarjetas de hemoglobina, leucocitos y plaquetas + botón **Send monthly panel to station** (antes existían en código pero eran inalcanzables). Botón **Pair Bluetooth sensor** visible. | 🟢 | En el navegador: hemoglobina guardada y panel mensual confirmado por la estación (“Monthly clinical panel synced”). |
| G. Mediciones manuales que sobreviven al cerrar/abrir (Fase 1) + historial de 30 días | ✅ | Prueba de importación y límite de historial. |

Pruebas de la app en CI: `for t in tools/tests/*.test.mjs; do node "$t"; done` (36 pruebas) + 35 pruebas Go.

## Pendiente

| Pendiente | Prioridad |
|---|---|
| **F. Bluetooth**: lista de dispositivos, batería, reconexión, procedencia por canal (el adaptador GATT estándar ya existe en `ble.js`). | P1 |
| **C. Análisis periódicos**: variación de peso automática y cumplimiento de ejercicio. | P1 |
| Procedencia **por métrica** en lo que se envía a la estación (hoy por paquete, criterio conservador). | P1 |
| Abrir la app sin red desde cero (service worker). | P1 |
| Unificar los clasificadores duplicados (config / risk / log / Go). | P2 |
