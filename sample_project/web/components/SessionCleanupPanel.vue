<template>
  <div class="session_cleanup">
    <h3>SessionCleanup</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// SessionCleanupPanel — панель «сессия»: удаляет просроченные токены.
export default {
  name: "SessionCleanupPanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (сессия).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.session_cleanup { padding: 0.5rem; }
</style>
